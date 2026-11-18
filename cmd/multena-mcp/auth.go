package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

// allowedAlgs is the fixed signature allowlist for incoming user tokens.
// Keycloak realm keys are RS256 by default; ES256 is accepted for completeness.
// Not configurable on purpose.
var allowedAlgs = []string{"RS256", "ES256"}

// bearerInfoKey is the TokenInfo.Extra key under which the inbound token is kept
// for the exchange step. It is never logged or returned to the client.
const bearerInfoKey = "bearer"

// tokenVerifier validates inbound Keycloak access tokens for this MCP server.
//
// Rules (fail closed, see Token-Contract t_c33696d0 §3):
//   - signature via JWKS, alg in allowlist, kid required
//   - iss == configured issuer
//   - aud contains configured MCP audience
//   - exp required and not elapsed
//   - sub non-empty
type tokenVerifier struct {
	keys     keyfunc.Keyfunc
	issuer   string
	audience string
}

func newTokenVerifier(ctx context.Context, cfg config) (*tokenVerifier, error) {
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{cfg.JWKSURL})
	if err != nil {
		return nil, fmt.Errorf("create JWKS keyfunc: %w", err)
	}
	return &tokenVerifier{keys: kf, issuer: cfg.Issuer, audience: cfg.Audience}, nil
}

// verify implements auth.TokenVerifier. Any rejection wraps auth.ErrInvalidToken,
// which the SDK turns into 401 with a WWW-Authenticate header.
func (v *tokenVerifier) verify(_ context.Context, tokenString string, _ *http.Request) (*auth.TokenInfo, error) {
	claims := jwt.MapClaims{}
	tok, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if kid, _ := t.Header["kid"].(string); kid == "" {
			return nil, errors.New("token has no kid")
		}
		return v.keys.Keyfunc(t)
	},
		jwt.WithValidMethods(allowedAlgs),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
	}
	if !tok.Valid {
		return nil, fmt.Errorf("%w: token not valid", auth.ErrInvalidToken)
	}

	// Explicit re-checks: jwt/v5 treats a missing aud/iss as "not checked" in some
	// option combinations, so do not rely on the parser alone for the binding rules.
	aud, err := claims.GetAudience()
	if err != nil || !slices.Contains(aud, v.audience) {
		return nil, fmt.Errorf("%w: audience mismatch", auth.ErrInvalidToken)
	}
	if iss, _ := claims.GetIssuer(); iss != v.issuer {
		return nil, fmt.Errorf("%w: issuer mismatch", auth.ErrInvalidToken)
	}
	exp, err := claims.GetExpirationTime()
	if err != nil || exp == nil {
		return nil, fmt.Errorf("%w: exp missing", auth.ErrInvalidToken)
	}
	sub, _ := claims.GetSubject()
	if sub == "" {
		return nil, fmt.Errorf("%w: sub missing", auth.ErrInvalidToken)
	}

	return &auth.TokenInfo{
		Expiration: exp.Time,
		UserID:     sub,
		Extra:      map[string]any{bearerInfoKey: tokenString},
	}, nil
}
