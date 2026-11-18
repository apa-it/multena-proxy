package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testKID        = "k1"
	testIssuer     = "https://keycloak.test/realms/apa"
	testAudience   = "multena-mcp"
	testUpstreamAu = "multena"
	testClientID   = "multena-mcp"
	testClientSec  = "s3cret-test"
	exchangedToken = "exchanged-upstream-token"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

func signingKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	return testKey
}

// jwksHandler serves the public half of key under kid testKID.
func jwksHandler(key *rsa.PublicKey) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		doc := map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "kid": testKID, "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}
}

// userClaims returns a valid inbound token claim set; tests mutate it.
func userClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": testIssuer,
		"aud": testAudience,
		"sub": "user-alice",
		"exp": now.Add(5 * time.Minute).Unix(),
		"iat": now.Unix(),
	}
}

func signRS256(t *testing.T, claims jwt.MapClaims, kid string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if kid != "" {
		tok.Header["kid"] = kid
	}
	s, err := tok.SignedString(signingKey(t))
	require.NoError(t, err)
	return s
}

// fakeKeycloak implements the token endpoint for RFC 8693 exchange.
type fakeKeycloak struct {
	t        *testing.T
	calls    atomic.Int32
	failWith int // non-zero: respond with this HTTP status
	// last observed form values (for assertions)
	mu   sync.Mutex
	form url.Values
}

func (f *fakeKeycloak) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.form = r.PostForm
	f.mu.Unlock()
	if f.failWith != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.failWith)
		_, _ = w.Write([]byte(`{"error":"access_denied","error_description":"not permitted"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":      exchangedToken,
		"issued_token_type": tokenTypeAccessToken,
		"token_type":        "Bearer",
		"expires_in":        300,
	})
}

// fakeProxy implements /api/v1/label/si/values and records the Authorization header.
type fakeProxy struct {
	calls    atomic.Int32
	mu       sync.Mutex
	authSeen []string
	data     []string
}

func (p *fakeProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.calls.Add(1)
	p.mu.Lock()
	p.authSeen = append(p.authSeen, r.Header.Get("Authorization"))
	p.mu.Unlock()
	if r.URL.Path != siValuesPath {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+exchangedToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": p.data})
}

type harness struct {
	srv       *httptest.Server
	keycloak  *fakeKeycloak
	proxy     *fakeProxy
	kcSrv     *httptest.Server
	proxySrv  *httptest.Server
	jwksSrv   *httptest.Server
	cfg       config
	jwksCalls atomic.Int32
}

func newHarness(t *testing.T, kc *fakeKeycloak, px *fakeProxy) *harness {
	t.Helper()
	h := &harness{keycloak: kc, proxy: px}
	h.jwksSrv = httptest.NewServer(jwksHandler(&signingKey(t).PublicKey))
	t.Cleanup(h.jwksSrv.Close)
	h.kcSrv = httptest.NewServer(kc)
	t.Cleanup(h.kcSrv.Close)
	h.proxySrv = httptest.NewServer(px)
	t.Cleanup(h.proxySrv.Close)

	// The MCP server's own URL is needed for the metadata; build the handler
	// after the listener exists.
	var handler http.Handler
	h.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
	}))
	h.srv.Start()
	t.Cleanup(h.srv.Close)

	h.cfg = config{
		PublicURL:        h.srv.URL,
		Issuer:           testIssuer,
		JWKSURL:          h.jwksSrv.URL + "/certs",
		Audience:         testAudience,
		TokenEndpoint:    h.kcSrv.URL + "/token",
		ClientID:         testClientID,
		ClientSecret:     testClientSec,
		UpstreamAudience: testUpstreamAu,
		ProxyURL:         h.proxySrv.URL,
	}
	v, err := newTokenVerifier(context.Background(), h.cfg)
	require.NoError(t, err)
	tools := &siTool{
		tokens: &tokenExchanger{
			endpoint: h.cfg.TokenEndpoint, clientID: testClientID, clientSecret: testClientSec,
			audience: testUpstreamAu, http: &http.Client{Timeout: 5 * time.Second},
		},
		proxyURL: h.cfg.ProxyURL,
		http:     &http.Client{Timeout: 5 * time.Second},
	}
	handler = newHandler(h.cfg, tools, v)
	return h
}

func postMCP(t *testing.T, base, bearer string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+mcpPath, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// --- Discovery and 401 behaviour -------------------------------------------

func TestProtectedResourceMetadata(t *testing.T) {
	h := newHarness(t, &fakeKeycloak{t: t}, &fakeProxy{})
	for _, path := range []string{metadataPath, metadataPath + mcpPath} {
		resp, err := http.Get(h.srv.URL + path)
		require.NoError(t, err)
		var doc map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&doc))
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
		assert.Equal(t, h.srv.URL+mcpPath, doc["resource"], path)
		assert.Equal(t, []any{testIssuer}, doc["authorization_servers"], path)
	}
}

func TestMissingToken_Returns401WithResourceMetadata(t *testing.T) {
	h := newHarness(t, &fakeKeycloak{t: t}, &fakeProxy{})
	resp := postMCP(t, h.srv.URL, "")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	want := fmt.Sprintf(`Bearer resource_metadata=%q`, h.srv.URL+metadataPath)
	assert.Equal(t, want, resp.Header.Get("WWW-Authenticate"))
	assert.Equal(t, int32(0), h.keycloak.calls.Load())
}

func TestInvalidTokens_Return401(t *testing.T) {
	now := time.Now()
	hs256 := func() string {
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, userClaims(now))
		tok.Header["kid"] = testKID
		s, err := tok.SignedString([]byte("shared-secret-for-test-only-123456"))
		require.NoError(t, err)
		return s
	}
	noneAlg := func() string {
		tok := jwt.NewWithClaims(jwt.SigningMethodNone, userClaims(now))
		tok.Header["kid"] = testKID
		s, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)
		return s
	}
	mutate := func(f func(jwt.MapClaims)) jwt.MapClaims {
		c := userClaims(now)
		f(c)
		return c
	}
	withoutClaim := func(name string) jwt.MapClaims {
		c := userClaims(now)
		delete(c, name)
		return c
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	forgedTok := func() string {
		s, err := jwt.NewWithClaims(jwt.SigningMethodRS256, userClaims(now)).SignedString(otherKey)
		require.NoError(t, err)
		return s
	}

	cases := map[string]string{
		"wrong aud (multena instead of multena-mcp)": signRS256(t, mutate(func(c jwt.MapClaims) { c["aud"] = testUpstreamAu }), testKID),
		"aud missing":                        signRS256(t, withoutClaim("aud"), testKID),
		"wrong issuer":                       signRS256(t, mutate(func(c jwt.MapClaims) { c["iss"] = "https://evil.test/realms/x" }), testKID),
		"issuer missing":                     signRS256(t, withoutClaim("iss"), testKID),
		"expired":                            signRS256(t, mutate(func(c jwt.MapClaims) { c["exp"] = now.Add(-time.Hour).Unix() }), testKID),
		"exp missing":                        signRS256(t, withoutClaim("exp"), testKID),
		"sub empty":                          signRS256(t, mutate(func(c jwt.MapClaims) { c["sub"] = "" }), testKID),
		"kid missing":                        signRS256(t, userClaims(now), ""),
		"kid unknown":                        signRS256(t, userClaims(now), "not-in-jwks"),
		"signed by other key with known kid": forgedTok(),
		"alg HS256":                          hs256(),
		"alg none":                           noneAlg(),
		"garbage":                            "not.a.jwt",
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, &fakeKeycloak{t: t}, &fakeProxy{})
			resp := postMCP(t, h.srv.URL, tok)
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
			assert.Contains(t, resp.Header.Get("WWW-Authenticate"), `resource_metadata="`+h.srv.URL+metadataPath+`"`)
			assert.Equal(t, int32(0), h.keycloak.calls.Load(), "no exchange for rejected token")
			assert.Equal(t, int32(0), h.proxy.calls.Load(), "no upstream call for rejected token")
		})
	}
}

// --- Config -----------------------------------------------------------------

func TestLoadConfig(t *testing.T) {
	base := map[string]string{
		"MULTENA_MCP_PUBLIC_URL":     "https://mcp.example.test/",
		"MULTENA_MCP_ISSUER":         testIssuer,
		"MULTENA_MCP_JWKS_URL":       "https://keycloak.test/certs",
		"MULTENA_MCP_TOKEN_ENDPOINT": "https://keycloak.test/token",
		"MULTENA_MCP_CLIENT_ID":      testClientID,
		"MULTENA_MCP_CLIENT_SECRET":  testClientSec,
		"MULTENA_MCP_PROXY_URL":      "https://metrics-query.example.test/",
	}
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}

	cfg, err := loadConfig(env(base))
	require.NoError(t, err)
	assert.Equal(t, "https://mcp.example.test", cfg.PublicURL, "trailing slash trimmed")
	assert.Equal(t, "https://mcp.example.test/mcp", cfg.ResourceURL())
	assert.Equal(t, "https://mcp.example.test/.well-known/oauth-protected-resource", cfg.MetadataURL())
	assert.Equal(t, "https://metrics-query.example.test", cfg.ProxyURL)
	assert.Equal(t, "multena-mcp", cfg.Audience)
	assert.Equal(t, "multena", cfg.UpstreamAudience)
	assert.Equal(t, ":8090", cfg.ListenAddr)

	for _, key := range []string{"MULTENA_MCP_CLIENT_SECRET", "MULTENA_MCP_ISSUER", "MULTENA_MCP_PROXY_URL"} {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		delete(m, key)
		_, err := loadConfig(env(m))
		assert.Error(t, err, "missing %s must fail", key)
	}

	m := map[string]string{}
	for k, v := range base {
		m[k] = v
	}
	m["MULTENA_MCP_PROXY_URL"] = "http://plain.example.test"
	_, err = loadConfig(env(m))
	assert.ErrorContains(t, err, "MULTENA_MCP_PROXY_URL must be an absolute https URL")
}

// --- End-to-end with a real MCP client --------------------------------------

type bearerRoundTripper struct {
	token string
	next  http.RoundTripper
}

func (b bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}

func connectClient(t *testing.T, h *harness, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:   h.srv.URL + mcpPath,
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token, next: http.DefaultTransport}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	session, err := client.Connect(ctx, transport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestToolListsServiceInstances_TokenExchangedNotPassedThrough(t *testing.T) {
	px := &fakeProxy{data: []string{"973874", "973875"}}
	kc := &fakeKeycloak{t: t}
	h := newHarness(t, kc, px)

	inbound := signRS256(t, userClaims(time.Now()), testKID)
	session := connectClient(t, h, inbound)

	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	assert.Equal(t, "list_service_instances", tools.Tools[0].Name)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_service_instances", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool returned error: %+v", res.Content)

	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var out listSIOutput
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, []string{"973874", "973875"}, out.ServiceInstances)

	// Exchange: subject is the inbound token, requested audience is multena,
	// client is the MCP client (secret sent only to the token endpoint).
	kc.mu.Lock()
	form := kc.form
	kc.mu.Unlock()
	assert.Equal(t, grantTypeTokenExchange, form.Get("grant_type"))
	assert.Equal(t, inbound, form.Get("subject_token"))
	assert.Equal(t, tokenTypeAccessToken, form.Get("subject_token_type"))
	assert.Equal(t, testUpstreamAu, form.Get("audience"))
	assert.Equal(t, testClientID, form.Get("client_id"))

	// Upstream: exactly one call, carrying the exchanged token and never the inbound one.
	assert.Equal(t, int32(1), px.calls.Load())
	px.mu.Lock()
	defer px.mu.Unlock()
	require.Len(t, px.authSeen, 1)
	assert.Equal(t, "Bearer "+exchangedToken, px.authSeen[0])
	assert.NotContains(t, px.authSeen[0], inbound)
}

func TestToolReturnsError_WhenExchangeDenied(t *testing.T) {
	px := &fakeProxy{data: []string{"973874"}}
	kc := &fakeKeycloak{t: t, failWith: http.StatusBadRequest}
	h := newHarness(t, kc, px)

	session := connectClient(t, h, signRS256(t, userClaims(time.Now()), testKID))
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_service_instances", Arguments: map[string]any{}})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Equal(t, int32(1), kc.calls.Load())
	assert.Equal(t, int32(0), px.calls.Load(), "no upstream call without exchanged token")
}
