package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWithLoki_EmptyURL_SkipsRouteRegistration proves that with no Loki URL
// configured, WithLoki does not register any Loki routes (defense against
// accidentally exposing an unconfigured/unreachable upstream).
func TestWithLoki_EmptyURL_SkipsRouteRegistration(t *testing.T) {
	a := &App{Cfg: &Config{}, e: mux.NewRouter()}

	a = a.WithLoki()

	match := &mux.RouteMatch{}
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/query", nil)
	assert.False(t, a.e.Match(req, match), "no Loki route must be registered when Loki.URL is empty")
}

// TestWithThanos_EmptyURL_SkipsRouteRegistration mirrors the Loki case for
// Thanos routes.
func TestWithThanos_EmptyURL_SkipsRouteRegistration(t *testing.T) {
	a := &App{Cfg: &Config{}, e: mux.NewRouter()}

	a = a.WithThanos()

	match := &mux.RouteMatch{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/query", nil)
	assert.False(t, a.e.Match(req, match), "no Thanos route must be registered when Thanos.URL is empty")
}

// TestWithLoki_RegistersRoutes proves routes are registered once the Loki
// URL is configured, as a smoke check for the route table in routes.go.
func TestWithLoki_RegistersRoutes(t *testing.T) {
	a := &App{Cfg: &Config{Loki: LokiConfig{URL: "http://upstream.example"}}, e: mux.NewRouter()}

	a = a.WithLoki()

	match := &mux.RouteMatch{}
	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/query", nil)
	assert.True(t, a.e.Match(req, match))
}

// TestSetHeaders_NonTLS_SetsBearerAuthorization proves that for a non-mTLS
// upstream, setHeaders injects the service account token as a Bearer
// Authorization header and applies any extra configured headers.
func TestSetHeaders_NonTLS_SetsBearerAuthorization(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	setHeaders(r, false, map[string]string{"X-Extra": "v"}, "sat-token")

	assert.Equal(t, "Bearer sat-token", r.Header.Get("Authorization"))
	assert.Equal(t, "v", r.Header.Get("X-Extra"))
}

// TestSetHeaders_TLS_DoesNotOverrideAuthorization proves that when the
// upstream connection uses mutual TLS, setHeaders must NOT inject a Bearer
// token (the client cert carries the identity instead); an existing
// Authorization header, if any, is left untouched.
func TestSetHeaders_TLS_DoesNotOverrideAuthorization(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer original-token")

	setHeaders(r, true, nil, "sat-token")

	assert.Equal(t, "Bearer original-token", r.Header.Get("Authorization"),
		"setHeaders must not touch Authorization when tls (mTLS) is in use")
}

// TestHandler_AdminBypass_SkipsEnforcementAndStreams proves the admin-bypass
// path (validateLabels -> skip=true) streams straight to upstream without
// calling the query enforcer at all.
func TestHandler_AdminBypass_SkipsEnforcementAndStreams(t *testing.T) {
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	app, tokens := setupTestMain()
	app.Cfg.Admin.Bypass = true
	app.Cfg.Admin.Group = "admins"

	h := handler("query", LogQLEnforcer(struct{}{}), "tenant_id", upstream.URL, false, nil, &app)

	req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/query?query=up{tenant_id=\"forbidden\"}", nil)
	req.Header.Set("Authorization", "Bearer "+tokens["adminUserToken"])
	rr := httptest.NewRecorder()

	h(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.True(t, upstreamHit, "admin bypass must still reach the upstream")
	// The forbidden-looking query must survive unmodified because enforcement was skipped.
	assert.Contains(t, req.URL.RawQuery, "forbidden")
}
