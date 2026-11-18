package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeExchanger is an upstreamTokenSource with a fixed result.
type fakeExchanger struct {
	token string
	err   error
	seen  string
}

func (f *fakeExchanger) Exchange(_ context.Context, subject string) (string, error) {
	f.seen = subject
	return f.token, f.err
}

func callWithToken(token string) *mcp.CallToolRequest {
	req := &mcp.CallToolRequest{}
	req.Extra = &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{
		UserID: "user-alice",
		Extra:  map[string]any{bearerInfoKey: token},
	}}
	return req
}

func newTestSITool(tokens upstreamTokenSource, proxyURL string) *siTool {
	return &siTool{tokens: tokens, proxyURL: proxyURL, http: &http.Client{Timeout: 5 * time.Second}}
}

func TestHandle_NoAuthenticatedSession(t *testing.T) {
	tool := newTestSITool(&fakeExchanger{token: "up"}, "https://unused.invalid")

	cases := map[string]*mcp.CallToolRequest{
		"nil request":   nil,
		"no extra":      {},
		"no token info": {Extra: &mcp.RequestExtra{}},
		"empty bearer":  callWithToken(""),
		"bearer of wrong type": {Extra: &mcp.RequestExtra{TokenInfo: &auth.TokenInfo{
			Extra: map[string]any{bearerInfoKey: 42},
		}}},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			_, out, err := tool.handle(context.Background(), req, listSIInput{})
			require.Error(t, err)
			assert.EqualError(t, err, "no authenticated session")
			assert.Empty(t, out.ServiceInstances)
		})
	}
}

func TestHandle_ExchangeFailureIsOpaque(t *testing.T) {
	ex := &fakeExchanger{err: errTokenExchange}
	tool := newTestSITool(ex, "https://unused.invalid")

	_, _, err := tool.handle(context.Background(), callWithToken("inbound-secret"), listSIInput{})
	require.Error(t, err)
	assert.EqualError(t, err, "upstream authorization unavailable")
	assert.NotContains(t, err.Error(), "inbound-secret")
	assert.Equal(t, "inbound-secret", ex.seen, "the inbound token is the exchange subject")
}

func TestHandle_ProxyFailureIsOpaque(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("proxy-internal-detail"))
	}))
	t.Cleanup(proxy.Close)
	tool := newTestSITool(&fakeExchanger{token: "up-token"}, proxy.URL)

	res, out, err := tool.handle(context.Background(), callWithToken("in"), listSIInput{})
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Empty(t, out.ServiceInstances)
	assert.EqualError(t, err, "multena-proxy request failed")
	assert.NotContains(t, err.Error(), "proxy-internal-detail")
}

func TestFetchSIValues(t *testing.T) {
	t.Run("success sends exchanged bearer and returns data", func(t *testing.T) {
		var gotAuth, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
			_, _ = w.Write([]byte(`{"status":"success","data":["973874","973875"]}`))
		}))
		t.Cleanup(srv.Close)

		vals, err := newTestSITool(nil, srv.URL+"/").fetchSIValues(context.Background(), "up-token")
		require.NoError(t, err)
		assert.Equal(t, []string{"973874", "973875"}, vals)
		assert.Equal(t, "Bearer up-token", gotAuth)
		assert.Equal(t, siValuesPath, gotPath)
	})

	t.Run("null data becomes empty list", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"success","data":null}`))
		}))
		t.Cleanup(srv.Close)

		vals, err := newTestSITool(nil, srv.URL).fetchSIValues(context.Background(), "t")
		require.NoError(t, err)
		assert.Equal(t, []string{}, vals)
	})

	t.Run("non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		t.Cleanup(srv.Close)

		_, err := newTestSITool(nil, srv.URL).fetchSIValues(context.Background(), "t")
		assert.EqualError(t, err, "proxy returned status 503")
	})

	t.Run("undecodable body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("not json"))
		}))
		t.Cleanup(srv.Close)

		_, err := newTestSITool(nil, srv.URL).fetchSIValues(context.Background(), "t")
		require.Error(t, err)
		assert.ErrorContains(t, err, "decode proxy response")
	})

	t.Run("status not success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"status":"error","data":["973874"]}`))
		}))
		t.Cleanup(srv.Close)

		vals, err := newTestSITool(nil, srv.URL).fetchSIValues(context.Background(), "t")
		assert.EqualError(t, err, `proxy status "error"`)
		assert.Nil(t, vals, "data of a failed response must not leak to the caller")
	})

	t.Run("transport error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close()

		_, err := newTestSITool(nil, url).fetchSIValues(context.Background(), "t")
		require.Error(t, err)
	})

	t.Run("unparseable proxy URL", func(t *testing.T) {
		_, err := newTestSITool(nil, "http://[::1").fetchSIValues(context.Background(), "t")
		require.Error(t, err)
		assert.ErrorContains(t, err, "build proxy URL")
	})
}
