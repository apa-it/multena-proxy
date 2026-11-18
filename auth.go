package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/rs/zerolog/log"
	"golang.org/x/exp/maps"

	"github.com/golang-jwt/jwt/v5"
)

// OAuthToken represents the structure of an OAuth token.
// It holds user-related information extracted from the token.
type OAuthToken struct {
	Groups            []string `json:"-"`
	PreferredUsername string   `json:"preferred_username"`
	Email             string   `json:"email"`
	jwt.RegisteredClaims
}

// getToken retrieves the OAuth token from the incoming HTTP request.
// It extracts, parses, and validates the token from the Authorization header.
func getToken(r *http.Request, a *App) (OAuthToken, error) {
	authToken := r.Header.Get("Authorization")
	if authToken == "" {
		if a.Cfg.Alert.Enabled && r.Header.Get(a.Cfg.Alert.TokenHeader) != "" {
			authToken = r.Header.Get(a.Cfg.Alert.TokenHeader)
		} else {
			return OAuthToken{}, errors.New("no Authorization header found")
		}
	}
	// Never log the token value itself; it is a credential and must not depend on log level.
	splitToken := strings.Split(authToken, "Bearer")
	log.Trace().Int("parts", len(splitToken)).Msg("SplitToken")
	if len(splitToken) != 2 {
		return OAuthToken{}, errors.New("invalid Authorization header")
	}

	oauthToken, token, err := parseJwtToken(strings.TrimSpace(splitToken[1]), a)
	if err != nil {
		return OAuthToken{}, fmt.Errorf("error parsing token")
	}
	if !token.Valid {
		return OAuthToken{}, fmt.Errorf("invalid token")
	}
	return oauthToken, nil
}

// allowedJWTAlgorithms pins the signing algorithms accepted for incoming tokens.
// RS256 is what the Keycloak realm signs with; PS256 is what the static alert
// certificate uses. The list is fixed in code and not derived from the JWK "alg"
// field, so any other algorithm (HS*, ES*, RS384/512, none, ...) is rejected.
var allowedJWTAlgorithms = []string{"RS256", "PS256"}

// requireKid rejects tokens without a "kid" header before any key lookup.
// Without it, keyfunc tries every key in the combined JWKS (Keycloak and alert
// certificate), which would let a token signed for one path validate on the other.
func requireKid(kf jwt.Keyfunc) jwt.Keyfunc {
	return func(t *jwt.Token) (any, error) {
		if kid, _ := t.Header["kid"].(string); kid == "" {
			return nil, errors.New("token header has no kid")
		}
		return kf(t)
	}
}

// verifyIssuer enforces the expected issuer list. An empty list disables the
// check. When the check is active, a missing or non-matching iss fails closed.
func verifyIssuer(claims jwt.MapClaims, expected []string) error {
	if len(expected) == 0 {
		return nil
	}
	iss, err := claims.GetIssuer()
	if err != nil || iss == "" {
		return errors.New("token has no iss claim but an issuer is expected")
	}
	if !slices.Contains(expected, iss) {
		return errors.New("token issuer is not in web.expected_issuer")
	}
	return nil
}

// logJWTValidation states at startup which token checks are active, so a
// disabled audience/issuer check is visible in the logs.
func (a *App) logJWTValidation() {
	aud, iss := a.Cfg.Web.ExpectedAudience, a.Cfg.Web.ExpectedIssuer

	audState, issState := "disabled", "disabled"
	if len(aud) > 0 {
		audState = "enabled"
	}
	if len(iss) > 0 {
		issState = "enabled"
	}

	log.Info().
		Strs("jwt_algorithms", allowedJWTAlgorithms).
		Str("jwt_audience_check", audState).
		Strs("expected_audience", aud).
		Str("jwt_issuer_check", issState).
		Strs("expected_issuer", iss).
		Msg("JWT validation settings")
	if audState == "disabled" {
		log.Warn().Msg("JWT audience check is DISABLED (web.expected_audience is empty): tokens issued for other clients of the realm are accepted")
	}
	if issState == "disabled" {
		log.Warn().Msg("JWT issuer check is DISABLED (web.expected_issuer is empty)")
	}
}

// parseJwtToken parses the JWT token string and constructs an OAuthToken from the parsed claims.
// It returns the constructed OAuthToken, the parsed jwt.Token, and any error that occurred during parsing.
//
// Always enforced: allowed signing algorithms, a kid header and an exp claim.
// Enforced only when configured: web.expected_audience and web.expected_issuer.
func parseJwtToken(tokenString string, a *App) (OAuthToken, *jwt.Token, error) {
	var oAuthToken OAuthToken
	var claimsMap jwt.MapClaims

	token, err := jwt.ParseWithClaims(tokenString, &claimsMap, requireKid(a.Jwks.Keyfunc),
		jwt.WithValidMethods(allowedJWTAlgorithms),
		jwt.WithExpirationRequired(),
		jwt.WithAudience(a.Cfg.Web.ExpectedAudience...), // no-op when empty
	)
	if err != nil {
		log.Error().Err(err).Msg("Error parsing token")
		return oAuthToken, nil, err
	}

	if !token.Valid {
		log.Trace().Msg("Token is invalid")
	}

	if err := verifyIssuer(claimsMap, a.Cfg.Web.ExpectedIssuer); err != nil {
		log.Error().Err(err).Msg("Error validating token issuer")
		return oAuthToken, nil, err
	}

	if v, ok := claimsMap["preferred_username"].(string); ok {
		oAuthToken.PreferredUsername = v
		log.Trace().Str("preferred_username", v).Msg("PreferredUsername")
	}

	if v, ok := claimsMap["email"].(string); ok {
		if !strings.Contains(v, "@") {
			log.Warn().Str("email", v).Msg("Email does not contain '@', therefore not an email. Could be sus")
		}
		log.Trace().Str("email", v).Msg("Email")
		oAuthToken.Email = v
	}

	if v, ok := claimsMap[a.Cfg.Web.OAuthGroupName].([]interface{}); ok {
		for _, item := range v {
			if s, ok := item.(string); ok {
				log.Trace().Str("group", s).Msg("Group")
				oAuthToken.Groups = append(oAuthToken.Groups, s)
			}
		}
	}

	return oAuthToken, token, err
}

// validateLabels validates the labels in the OAuth token.
// It checks if the user is an admin and skips label enforcement if true.
// Returns a map representing valid labels, a boolean indicating whether label enforcement should be skipped,
// and any error that occurred during validation.
func validateLabels(ctx context.Context, token OAuthToken, a *App) (map[string]bool, bool, error) {
	// Defense in depth: an empty/zero-value token means authentication never
	// succeeded (e.g. getToken failed upstream). Reject unconditionally here
	// instead of relying solely on the later `len(tenantLabels) < 1` check,
	// which depends on the configured LabelStore returning no labels for an
	// anonymous identity and can be bypassed by a permissive or misconfigured
	// LabelStore.
	if token.PreferredUsername == "" && token.Email == "" && len(token.Groups) == 0 {
		return nil, false, fmt.Errorf("empty identity: authentication required")
	}

	if isAdmin(token, a) {
		log.Debug().Str("user", token.PreferredUsername).Bool("Admin", true).Msg("Skipping label enforcement")
		return nil, true, nil
	}

	tenantLabels, skip, err := a.LabelStore.GetLabels(ctx, token)
	if err != nil {
		return nil, false, fmt.Errorf("error retrieving labels: %w", err)
	}
	if skip {
		log.Debug().Str("user", token.PreferredUsername).Bool("Admin", false).Msg("Skipping label enforcement")
		return nil, true, nil
	}
	log.Debug().Str("user", token.PreferredUsername).Strs("labels", maps.Keys(tenantLabels)).Msg("")

	if len(tenantLabels) < 1 {
		return nil, false, fmt.Errorf("no tenant labels found")
	}
	return tenantLabels, false, nil
}

func isAdmin(token OAuthToken, a *App) bool {
	return ContainsIgnoreCase(token.Groups, a.Cfg.Admin.Group) && a.Cfg.Admin.Bypass
}
