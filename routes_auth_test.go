package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// permissiveLabelStore is a Labelstore stub that returns non-empty labels
// even for a completely empty/zero-value OAuthToken. A real LabelStore
// (ConfigMapHandler/MySQLHandler) would typically return zero labels for an
// anonymous identity, which is what currently stops an unauthenticated
// request at auth.go:110 (`len(tenantLabels) < 1`). That means a naive test
// of "no Authorization header -> 403" already passes today and proves
// nothing about whether the handler itself correctly stops processing after
// a failed getToken call.
//
// By making the LabelStore permissive even for an empty identity, this stub
// removes that incidental safety net so the test actually exercises the bug:
// if routes.go doesn't `return` after a failed getToken, the zero-value
// OAuthToken sails through validateLabels (and, were it not for the
// defense-in-depth guard added in auth.go, through to the upstream).
type permissiveLabelStore struct{}

func (permissiveLabelStore) Connect(App) error { return nil }

func (permissiveLabelStore) GetLabels(_ context.Context, _ OAuthToken) (map[string]bool, bool, error) {
	return map[string]bool{"allowed_tenant": true}, false, nil
}

// TestHandler_AuthFailure_NeverReachesUpstream is the red/green test for the
// missing `return` after a failed getToken in routes.go's handler().
//
// Red (before the fix): the fake upstream gets hit and/or the "superfluous
// WriteHeader" warning is logged, because processing continues past the
// failed getToken call using a zero-value OAuthToken, and the permissive
// LabelStore stub above hands out labels for it instead of rejecting it.
//
// Green (after the fix): the handler returns immediately after
// logAndWriteError, with 403 and the upstream never touched.
func TestHandler_AuthFailure_NeverReachesUpstream(t *testing.T) {
	upstreamHit := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	app := &App{
		Cfg:        &Config{},
		LabelStore: permissiveLabelStore{},
	}

	h := handler("query", LogQLEnforcer(struct{}{}), "tenant", upstream.URL, false, nil, app)

	testCases := []struct {
		name          string
		authorization string
		setHeader     bool
	}{
		{name: "missing Authorization header", setHeader: false},
		{name: "invalid Authorization header", authorization: "not-a-bearer-token", setHeader: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			upstreamHit = false
			req := httptest.NewRequest(http.MethodGet, "/loki/api/v1/query?query=1", nil)
			if tc.setHeader {
				req.Header.Set("Authorization", tc.authorization)
			}
			rr := httptest.NewRecorder()

			h(rr, req)

			assert.Equal(t, http.StatusForbidden, rr.Code, "must reject requests with a failed/missing token")
			assert.False(t, upstreamHit, "upstream must never be called after a failed authentication")
		})
	}
}

// TestValidateLabels_EmptyIdentity_RejectsIndependentlyOfLabelStore proves the
// defense-in-depth guard in validateLabels: even with a permissive LabelStore
// that would happily hand out labels for a zero-value token, an empty
// identity must be rejected on its own, without depending on
// `len(tenantLabels) < 1`.
func TestValidateLabels_EmptyIdentity_RejectsIndependentlyOfLabelStore(t *testing.T) {
	app := &App{
		Cfg:        &Config{},
		LabelStore: permissiveLabelStore{},
	}

	labels, skip, err := validateLabels(context.Background(), OAuthToken{}, app)

	assert.Error(t, err)
	assert.False(t, skip)
	assert.Nil(t, labels)
}
