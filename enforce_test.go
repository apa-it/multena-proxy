package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubEnforceQL is a controllable EnforceQL stub used to isolate
// enforceRequest/enforceGet/enforcePost from the real LogQL/PromQL parsers.
type stubEnforceQL struct {
	result string
	err    error
}

func (s stubEnforceQL) Enforce(_ string, _ map[string]bool, _ string) (string, error) {
	return s.result, s.err
}

// TestEnforceRequest_DispatchesByMethod proves enforceRequest routes GET to
// enforceGet, POST to enforcePost, and rejects any other method outright
// instead of silently falling through to one of the two enforcement paths.
func TestEnforceRequest_DispatchesByMethod(t *testing.T) {
	t.Run("GET uses enforceGet (query string rewritten)", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/x?query=orig", nil)
		err := enforceRequest(r, stubEnforceQL{result: "enforced"}, map[string]bool{"a": true}, "tenant", "query")
		require.NoError(t, err)
		assert.Equal(t, "enforced", r.URL.Query().Get("query"))
	})

	t.Run("POST uses enforcePost (form body rewritten)", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("query=orig"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		err := enforceRequest(r, stubEnforceQL{result: "enforced"}, map[string]bool{"a": true}, "tenant", "query")
		require.NoError(t, err)
		body, _ := io.ReadAll(r.Body)
		assert.Equal(t, "query=enforced", string(body))
	})

	t.Run("unsupported method is rejected", func(t *testing.T) {
		for _, m := range []string{http.MethodPut, http.MethodDelete, http.MethodPatch, http.MethodHead, http.MethodOptions} {
			r := httptest.NewRequest(m, "/x", nil)
			err := enforceRequest(r, stubEnforceQL{result: "enforced"}, map[string]bool{"a": true}, "tenant", "query")
			assert.Error(t, err, "method %s must be rejected", m)
			assert.Equal(t, "invalid method", err.Error())
		}
	})
}

// TestEnforcePost_RewritesFormBody_QueryMatch covers the Loki/Thanos "query"
// form-field case (query_range, query, tail, index/stats, format_query,
// query_exemplars, status/buildinfo, metadata, and Loki labels/label/{x}/values).
func TestEnforcePost_RewritesFormBody_QueryMatch(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/loki/api/v1/query_range", strings.NewReader("query=%7Btenant%3D%22x%22%7D&start=1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	err := enforcePost(r, stubEnforceQL{result: `{tenant="allowed"}`}, map[string]bool{"allowed": true}, "tenant", "query")
	require.NoError(t, err)

	body, err := io.ReadAll(r.Body)
	require.NoError(t, err)

	values, err := url.ParseQuery(string(body))
	require.NoError(t, err)
	assert.Equal(t, `{tenant="allowed"}`, values.Get("query"))
	assert.Empty(t, r.URL.RawQuery, "query string must be cleared, only the body carries the enforced query")
	assert.Equal(t, int64(len(body)), r.ContentLength, "ContentLength must be recomputed for the rewritten body")
}

// TestEnforcePost_RewritesFormBody_MatchArray covers the match[] form-field
// case used by /api/v1/series and (for Thanos) /api/v1/labels and
// /api/v1/label/{label}/values.
func TestEnforcePost_RewritesFormBody_MatchArray(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/series", strings.NewReader(`match%5B%5D=up`))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	err := enforcePost(r, stubEnforceQL{result: `up{tenant="allowed"}`}, map[string]bool{"allowed": true}, "tenant", "match[]")
	require.NoError(t, err)

	body, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	values, err := url.ParseQuery(string(body))
	require.NoError(t, err)
	assert.Equal(t, `up{tenant="allowed"}`, values.Get("match[]"))
}

// TestEnforcePost_EnforceError proves a rejection from the underlying
// enforcer (e.g. cross-tenant query) stops processing before the body is
// ever rewritten, so the original, unauthorized query never reaches the body.
func TestEnforcePost_EnforceError(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/loki/api/v1/query_range", strings.NewReader(`query={tenant="forbidden"}`))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	err := enforcePost(r, stubEnforceQL{err: errors.New("unauthorized label forbidden")}, map[string]bool{"allowed": true}, "tenant", "query")

	require.Error(t, err)
	assert.Equal(t, "unauthorized label forbidden", err.Error())
}

// erroringBody is an io.ReadCloser whose Read always fails, used to force
// http.Request.ParseForm into its error path.
type erroringBody struct{}

func (erroringBody) Read(_ []byte) (int, error) { return 0, errors.New("simulated read failure") }
func (erroringBody) Close() error               { return nil }

// TestEnforcePost_InvalidForm proves a malformed/unreadable request body
// surfaces as an error instead of panicking or silently proceeding with an
// empty/garbage form.
func TestEnforcePost_InvalidForm(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/loki/api/v1/query_range", nil)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Body = erroringBody{}
	r.ContentLength = -1

	err := enforcePost(r, stubEnforceQL{result: "irrelevant"}, map[string]bool{"allowed": true}, "tenant", "query")

	assert.Error(t, err)
}

// TestEnforcePost_EmptyQuery proves an absent query/match[] value in the body
// still goes through the enforcer (which synthesizes a tenant-scoped query),
// mirroring enforceGet's handling of an empty query parameter.
func TestEnforcePost_EmptyQuery(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/loki/api/v1/query", strings.NewReader(""))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	err := enforcePost(r, stubEnforceQL{result: `{tenant="allowed"}`}, map[string]bool{"allowed": true}, "tenant", "query")
	require.NoError(t, err)

	body, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	values, err := url.ParseQuery(string(body))
	require.NoError(t, err)
	assert.Equal(t, `{tenant="allowed"}`, values.Get("query"))
}
