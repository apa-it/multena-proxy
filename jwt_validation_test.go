package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAudience = "multena-proxy"
	testIssuer   = "https://sso.example.com/realms/internal"
)

// rsaJWKSJSON renders pub as a single-key JWKS document (kid testKid) whose
// alg field is alg. keyfunc requires the token alg to match the JWK alg.
func rsaJWKSJSON(pub *rsa.PublicKey, alg string) string {
	n := base64.RawURLEncoding.EncodeToString(pub.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes())
	return fmt.Sprintf(`{"keys":[{"kty":"RSA","kid":"testKid","alg":"%s","use":"sig","n":"%s","e":"%s"}]}`, alg, n, e)
}

// newJWTTestApp builds an App whose JWKS (alg RS256) serves pub and whose
// aud/iss expectations are set to the given values (nil = check disabled).
func newJWTTestApp(t *testing.T, pub *rsa.PublicKey, aud, iss []string) *App {
	t.Helper()
	return newJWTTestAppWithAlg(t, pub, "RS256", aud, iss)
}

func newJWTTestAppWithAlg(t *testing.T, pub *rsa.PublicKey, alg string, aud, iss []string) *App {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, rsaJWKSJSON(pub, alg))
	}))
	t.Cleanup(srv.Close)

	app := &App{}
	app.WithConfig()
	app.Cfg.Web.JwksCertURL = srv.URL
	app.Cfg.Web.ExpectedAudience = aud
	app.Cfg.Web.ExpectedIssuer = iss
	app.WithJWKS()
	return app
}

// validClaims returns a complete, unexpired claim set. aud and iss are only
// set when non-nil / non-empty so tests can omit them.
func validClaims(aud any, iss string) jwt.MapClaims {
	c := jwt.MapClaims{
		"preferred_username": "user",
		"email":              "user@example.com",
		"groups":             []string{"group1"},
		"exp":                time.Now().Add(time.Hour).Unix(),
	}
	if aud != nil {
		c["aud"] = aud
	}
	if iss != "" {
		c["iss"] = iss
	}
	return c
}

// signTestToken signs claims with method and key. kid is set in the header
// only when non-empty.
func signTestToken(t *testing.T, method jwt.SigningMethod, key any, claims jwt.MapClaims, kid string) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, claims)
	if kid != "" {
		tok.Header["kid"] = kid
	}
	s, err := tok.SignedString(key)
	require.NoError(t, err)
	return s
}

func newRSAKeyForTest(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

// --- audience ---------------------------------------------------------------

func TestParseJwtToken_Audience_MismatchRejected(t *testing.T) {
	key := newRSAKeyForTest(t)
	app := newJWTTestApp(t, &key.PublicKey, []string{testAudience}, nil)

	tok := signTestToken(t, jwt.SigningMethodRS256, key, validClaims("some-other-client", testIssuer), "testKid")
	_, _, err := parseJwtToken(tok, app)
	assert.Error(t, err, "token issued for a different audience must be rejected")
}

func TestParseJwtToken_Audience_MatchAccepted(t *testing.T) {
	key := newRSAKeyForTest(t)
	app := newJWTTestApp(t, &key.PublicKey, []string{testAudience}, nil)

	tok := signTestToken(t, jwt.SigningMethodRS256, key, validClaims(testAudience, testIssuer), "testKid")
	oauth, _, err := parseJwtToken(tok, app)
	assert.NoError(t, err)
	assert.Equal(t, "user", oauth.PreferredUsername)
}

func TestParseJwtToken_Audience_AnyOfListAccepted(t *testing.T) {
	key := newRSAKeyForTest(t)
	app := newJWTTestApp(t, &key.PublicKey, []string{"other-client", testAudience}, nil)

	tok := signTestToken(t, jwt.SigningMethodRS256, key, validClaims([]string{"unrelated", testAudience}, testIssuer), "testKid")
	_, _, err := parseJwtToken(tok, app)
	assert.NoError(t, err)
}

func TestParseJwtToken_Audience_MissingRejectedWhenConfigured(t *testing.T) {
	key := newRSAKeyForTest(t)
	app := newJWTTestApp(t, &key.PublicKey, []string{testAudience}, nil)

	tok := signTestToken(t, jwt.SigningMethodRS256, key, validClaims(nil, testIssuer), "testKid")
	_, _, err := parseJwtToken(tok, app)
	assert.Error(t, err, "a missing aud claim must fail closed when an audience is expected")
}

// --- issuer -----------------------------------------------------------------

func TestParseJwtToken_Issuer_MismatchRejected(t *testing.T) {
	key := newRSAKeyForTest(t)
	app := newJWTTestApp(t, &key.PublicKey, nil, []string{testIssuer})

	tok := signTestToken(t, jwt.SigningMethodRS256, key, validClaims(testAudience, "https://evil.example.com/realms/other"), "testKid")
	_, _, err := parseJwtToken(tok, app)
	assert.Error(t, err, "token from a non-expected issuer must be rejected")
}

func TestParseJwtToken_Issuer_MatchAccepted(t *testing.T) {
	key := newRSAKeyForTest(t)
	app := newJWTTestApp(t, &key.PublicKey, nil, []string{testIssuer})

	tok := signTestToken(t, jwt.SigningMethodRS256, key, validClaims(testAudience, testIssuer), "testKid")
	_, _, err := parseJwtToken(tok, app)
	assert.NoError(t, err)
}

func TestParseJwtToken_Issuer_MissingRejectedWhenConfigured(t *testing.T) {
	key := newRSAKeyForTest(t)
	app := newJWTTestApp(t, &key.PublicKey, nil, []string{testIssuer})

	tok := signTestToken(t, jwt.SigningMethodRS256, key, validClaims(testAudience, ""), "testKid")
	_, _, err := parseJwtToken(tok, app)
	assert.Error(t, err, "a missing iss claim must fail closed when an issuer is expected")
}

// --- regression: empty aud/iss configuration keeps the old behaviour --------

func TestParseJwtToken_EmptyAudIssConfig_AcceptsAnyAudAndIss(t *testing.T) {
	key := newRSAKeyForTest(t)
	app := newJWTTestApp(t, &key.PublicKey, nil, nil)

	tok := signTestToken(t, jwt.SigningMethodRS256, key, validClaims("whatever-client", "https://any.example.com"), "testKid")
	_, _, err := parseJwtToken(tok, app)
	assert.NoError(t, err, "empty expected_audience/expected_issuer must not change existing behaviour")
}

// --- mandatory claims and key id: always on ---------------------------------

func TestParseJwtToken_MissingExp_Rejected(t *testing.T) {
	key := newRSAKeyForTest(t)
	for _, configured := range []bool{false, true} {
		var aud, iss []string
		if configured {
			aud, iss = []string{testAudience}, []string{testIssuer}
		}
		app := newJWTTestApp(t, &key.PublicKey, aud, iss)

		claims := validClaims(testAudience, testIssuer)
		delete(claims, "exp")
		tok := signTestToken(t, jwt.SigningMethodRS256, key, claims, "testKid")
		_, _, err := parseJwtToken(tok, app)
		assert.Error(t, err, "token without exp must be rejected (configured=%v)", configured)
	}
}

func TestParseJwtToken_MissingKid_Rejected(t *testing.T) {
	key := newRSAKeyForTest(t)
	for _, configured := range []bool{false, true} {
		var aud, iss []string
		if configured {
			aud, iss = []string{testAudience}, []string{testIssuer}
		}
		app := newJWTTestApp(t, &key.PublicKey, aud, iss)

		tok := signTestToken(t, jwt.SigningMethodRS256, key, validClaims(testAudience, testIssuer), "")
		_, _, err := parseJwtToken(tok, app)
		assert.Error(t, err, "token without kid must not be tried against all keys (configured=%v)", configured)
	}
}

// --- algorithm pinning: always on, regardless of aud/iss config -------------

func TestParseJwtToken_AllowedAlgorithms_Accepted(t *testing.T) {
	// RS256: Keycloak realm signing. PS256: static alert certificate in ocpmgmt
	// (its JWK alg is PS256, so the JWKS for that case must advertise PS256).
	key := newRSAKeyForTest(t)

	for _, method := range []jwt.SigningMethod{jwt.SigningMethodRS256, jwt.SigningMethodPS256} {
		app := newJWTTestAppWithAlg(t, &key.PublicKey, method.Alg(), nil, nil)
		tok := signTestToken(t, method, key, validClaims(testAudience, testIssuer), "testKid")
		_, _, err := parseJwtToken(tok, app)
		assert.NoError(t, err, "%s must be accepted", method.Alg())
	}
}

func TestParseJwtToken_UnexpectedAlgorithm_Rejected(t *testing.T) {
	key := newRSAKeyForTest(t)

	cases := []struct {
		name   string
		method jwt.SigningMethod
		key    any
	}{
		{"RS384_signed_with_valid_key", jwt.SigningMethodRS384, key},
		{"RS512_signed_with_valid_key", jwt.SigningMethodRS512, key},
		{"PS512_signed_with_valid_key", jwt.SigningMethodPS512, key},
		{"HS256_with_shared_secret", jwt.SigningMethodHS256, []byte("secret")},
		{"none", jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType},
	}

	for _, configured := range []bool{false, true} {
		var aud, iss []string
		if configured {
			aud, iss = []string{testAudience}, []string{testIssuer}
		}
		app := newJWTTestApp(t, &key.PublicKey, aud, iss)

		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s_configured=%v", tc.name, configured), func(t *testing.T) {
				tok := signTestToken(t, tc.method, tc.key, validClaims(testAudience, testIssuer), "testKid")
				oauth, _, err := parseJwtToken(tok, app)
				assert.Error(t, err, "algorithm %s must be rejected", tc.method.Alg())
				assert.Equal(t, OAuthToken{}, oauth)
			})
		}
	}
}

// --- startup log states whether the checks are active -----------------------

func TestWithJWKS_LogsValidationState(t *testing.T) {
	key := newRSAKeyForTest(t)

	var disabled bytes.Buffer
	captureLog(t, &disabled, func() {
		newJWTTestApp(t, &key.PublicKey, nil, nil)
	})
	assert.Contains(t, disabled.String(), `"jwt_audience_check":"disabled"`)
	assert.Contains(t, disabled.String(), `"jwt_issuer_check":"disabled"`)
	assert.Contains(t, disabled.String(), `"jwt_algorithms":["RS256","PS256"]`)
	assert.Contains(t, disabled.String(), `"level":"warn"`, "disabled checks must produce a warning")

	var enabled bytes.Buffer
	captureLog(t, &enabled, func() {
		newJWTTestApp(t, &key.PublicKey, []string{testAudience}, []string{testIssuer})
	})
	assert.Contains(t, enabled.String(), `"jwt_audience_check":"enabled"`)
	assert.Contains(t, enabled.String(), `"jwt_issuer_check":"enabled"`)
	assert.Contains(t, enabled.String(), testAudience)
	assert.Contains(t, enabled.String(), testIssuer)
}
