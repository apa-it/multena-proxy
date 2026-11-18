package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// leakMarker is placed in every failing Keycloak response. It must never show
// up in the error returned by Exchange, because that error can reach logs and,
// via the tool layer, must not reach the MCP client.
const leakMarker = "LEAK-MARKER-9f3c"

func newTestExchanger(endpoint string) *tokenExchanger {
	return &tokenExchanger{
		endpoint:     endpoint,
		clientID:     testClientID,
		clientSecret: testClientSec,
		audience:     testUpstreamAu,
		http:         &http.Client{Timeout: 5 * time.Second},
	}
}

func TestExchange_Success(t *testing.T) {
	kc := &fakeKeycloak{t: t}
	srv := httptest.NewServer(kc)
	t.Cleanup(srv.Close)

	tok, err := newTestExchanger(srv.URL+"/token").Exchange(context.Background(), "inbound-abc")
	require.NoError(t, err)
	assert.Equal(t, exchangedToken, tok)

	kc.mu.Lock()
	defer kc.mu.Unlock()
	assert.Equal(t, grantTypeTokenExchange, kc.form.Get("grant_type"))
	assert.Equal(t, "inbound-abc", kc.form.Get("subject_token"))
	assert.Equal(t, tokenTypeAccessToken, kc.form.Get("subject_token_type"))
	assert.Equal(t, tokenTypeAccessToken, kc.form.Get("requested_token_type"))
	assert.Equal(t, testUpstreamAu, kc.form.Get("audience"))
	assert.Equal(t, testClientID, kc.form.Get("client_id"))
	assert.Equal(t, testClientSec, kc.form.Get("client_secret"))
}

func TestExchange_TokenTypeIsCaseInsensitive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"up-1","token_type":"BEARER"}`))
	}))
	t.Cleanup(srv.Close)

	tok, err := newTestExchanger(srv.URL).Exchange(context.Background(), "in")
	require.NoError(t, err)
	assert.Equal(t, "up-1", tok)
}

// Every failure path must return errTokenExchange, an empty token, and no
// Keycloak payload in the message.
func TestExchange_FailuresAreOpaque(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"status 400 with error body": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"access_denied","error_description":"` + leakMarker + `"}`))
		},
		"status 401": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(leakMarker))
		},
		"status 500": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
		"status 204 without body": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
		"200 but not JSON": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>" + leakMarker + "</html>"))
		},
		"200 with empty body": func(w http.ResponseWriter, _ *http.Request) {},
		"200 with empty access_token": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"access_token":"","token_type":"Bearer","note":"` + leakMarker + `"}`))
		},
		"200 with missing access_token": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"token_type":"Bearer"}`))
		},
		"token_type mac": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"access_token":"` + leakMarker + `","token_type":"mac"}`))
		},
		"token_type missing": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"access_token":"` + leakMarker + `"}`))
		},
		"body above size limit": func(w http.ResponseWriter, _ *http.Request) {
			// The reader is capped at maxTokenResponseBytes, so the JSON is cut off.
			_, _ = w.Write([]byte(`{"access_token":"` + strings.Repeat("a", maxTokenResponseBytes) + `","token_type":"bearer"}`))
		},
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			t.Cleanup(srv.Close)

			tok, err := newTestExchanger(srv.URL).Exchange(context.Background(), "inbound")
			require.Error(t, err)
			assert.Empty(t, tok)
			assert.True(t, errors.Is(err, errTokenExchange), "error must wrap errTokenExchange, got %v", err)
			assert.NotContains(t, err.Error(), leakMarker)
			assert.NotContains(t, err.Error(), "inbound")
		})
	}
}

func TestExchange_TransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := srv.URL
	srv.Close() // nothing listens any more: connection refused

	tok, err := newTestExchanger(endpoint).Exchange(context.Background(), "inbound")
	require.Error(t, err)
	assert.Empty(t, tok)
	assert.True(t, errors.Is(err, errTokenExchange))
	assert.ErrorContains(t, err, "transport")
}

func TestExchange_InvalidEndpoint(t *testing.T) {
	// A control character makes http.NewRequest fail before any network I/O.
	tok, err := newTestExchanger("http://\x7f.invalid/token").Exchange(context.Background(), "inbound")
	require.Error(t, err)
	assert.Empty(t, tok)
	assert.Equal(t, errTokenExchange, err)
}
