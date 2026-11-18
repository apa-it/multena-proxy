package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// If the JWKS cannot be loaded at startup, keyfunc logs the error and the
// verifier still starts, but it holds no keys. Every token then fails on
// "unknown kid", so the endpoint stays fail-closed.
func TestNewTokenVerifier_UnreachableJWKSFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	v, err := newTokenVerifier(context.Background(), config{
		Issuer: testIssuer, Audience: testAudience, JWKSURL: srv.URL + "/certs",
	})
	require.NoError(t, err)

	tok := signRS256(t, userClaims(time.Now()), testKID)
	info, err := v.verify(context.Background(), tok, nil)
	assert.Nil(t, info)
	assert.True(t, errors.Is(err, auth.ErrInvalidToken), "got %v", err)
}

func TestNewTokenVerifier_RejectsMalformedJWKSURL(t *testing.T) {
	_, err := newTokenVerifier(context.Background(), config{JWKSURL: "http://[::1"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "create JWKS keyfunc")
}

func TestAllowedAlgs_RS256Only(t *testing.T) {
	// Keycloak signs access tokens with RS256 (see comment on allowedAlgs).
	assert.Equal(t, []string{jwt.SigningMethodRS256.Alg()}, allowedAlgs)
}
