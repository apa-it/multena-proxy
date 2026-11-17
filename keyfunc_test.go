package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewCombinedJwks_InvalidURL proves a malformed JWKS URL is surfaced as
// an error instead of panicking or returning a usable-looking keyfunc.
func TestNewCombinedJwks_InvalidURL(t *testing.T) {
	_, err := NewCombinedJwks(context.Background(), []string{"://not-a-url"}, nil)
	assert.Error(t, err)
}

// TestNewCombinedJwks_InvalidRawJWKS proves malformed inline JWKS material
// (as used for the alert-webhook static certificate) is rejected with
// ErrKeyfunc instead of being silently ignored.
func TestNewCombinedJwks_InvalidRawJWKS(t *testing.T) {
	_, err := NewCombinedJwks(context.Background(), []string{"http://127.0.0.1:1/jwks"}, []byte(`{not-json`))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrKeyfunc)
}

// TestNewCombinedJwks_EmptyRawJWKS_StillUsesURLStorage proves that when no
// static raw JWKS is configured (the common case), construction only
// depends on the HTTP-based key storage and does not require a reachable
// JWKS endpoint up front (keyfunc.New builds lazily).
func TestNewCombinedJwks_EmptyRawJWKS_StillUsesURLStorage(t *testing.T) {
	kf, err := NewCombinedJwks(context.Background(), []string{"http://127.0.0.1:1/jwks"}, nil)
	require.NoError(t, err)
	assert.NotNil(t, kf)
}
