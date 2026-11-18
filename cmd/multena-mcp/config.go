package main

import (
	"fmt"
	"net/url"
	"strings"
)

// config holds the settings of the multena-mcp resource server.
//
// Everything is read from MULTENA_MCP_* environment variables. Missing
// required values are a startup error (fail closed); there are no defaults
// for URLs, issuer or client credentials.
type config struct {
	ListenAddr string
	// PublicURL is the externally reachable base URL of this server
	// (no path), e.g. https://multena-mcp.example.apa.at.
	PublicURL string
	// Issuer is the Keycloak realm issuer. Tokens must carry exactly this iss.
	Issuer string
	// JWKSURL is the Keycloak JWKS endpoint used to verify incoming tokens.
	JWKSURL string
	// Audience is the aud this MCP server accepts on incoming tokens.
	Audience string
	// TokenEndpoint is the Keycloak token endpoint used for RFC 8693 exchange.
	TokenEndpoint string
	// ClientID / ClientSecret authenticate this server at the token endpoint.
	ClientID     string
	ClientSecret string
	// UpstreamAudience is the aud requested in the exchange (multena's client id).
	UpstreamAudience string
	// ProxyURL is the base URL of multena-proxy, e.g. https://metrics-query.apa-it.at.
	ProxyURL string
}

// ResourceURL is the canonical identifier of the MCP endpoint (RFC 9728 "resource").
func (c config) ResourceURL() string { return c.PublicURL + mcpPath }

// MetadataURL is the protected-resource-metadata URL advertised in WWW-Authenticate.
func (c config) MetadataURL() string { return c.PublicURL + metadataPath }

func loadConfig(getenv func(string) string) (config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	c := config{
		ListenAddr:       get("MULTENA_MCP_LISTEN_ADDR", ":8090"),
		PublicURL:        strings.TrimRight(get("MULTENA_MCP_PUBLIC_URL", ""), "/"),
		Issuer:           get("MULTENA_MCP_ISSUER", ""),
		JWKSURL:          get("MULTENA_MCP_JWKS_URL", ""),
		Audience:         get("MULTENA_MCP_AUDIENCE", "multena-mcp"),
		TokenEndpoint:    get("MULTENA_MCP_TOKEN_ENDPOINT", ""),
		ClientID:         get("MULTENA_MCP_CLIENT_ID", ""),
		ClientSecret:     get("MULTENA_MCP_CLIENT_SECRET", ""),
		UpstreamAudience: get("MULTENA_MCP_UPSTREAM_AUDIENCE", "multena"),
		ProxyURL:         strings.TrimRight(get("MULTENA_MCP_PROXY_URL", ""), "/"),
	}
	if err := c.validate(); err != nil {
		return config{}, err
	}
	return c, nil
}

func (c config) validate() error {
	required := map[string]string{
		"MULTENA_MCP_PUBLIC_URL":     c.PublicURL,
		"MULTENA_MCP_ISSUER":         c.Issuer,
		"MULTENA_MCP_JWKS_URL":       c.JWKSURL,
		"MULTENA_MCP_TOKEN_ENDPOINT": c.TokenEndpoint,
		"MULTENA_MCP_CLIENT_ID":      c.ClientID,
		"MULTENA_MCP_CLIENT_SECRET":  c.ClientSecret,
		"MULTENA_MCP_PROXY_URL":      c.ProxyURL,
	}
	for name, val := range required {
		if val == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	httpsOnly := map[string]string{
		"MULTENA_MCP_PUBLIC_URL":     c.PublicURL,
		"MULTENA_MCP_ISSUER":         c.Issuer,
		"MULTENA_MCP_JWKS_URL":       c.JWKSURL,
		"MULTENA_MCP_TOKEN_ENDPOINT": c.TokenEndpoint,
		"MULTENA_MCP_PROXY_URL":      c.ProxyURL,
	}
	for name, val := range httpsOnly {
		u, err := url.Parse(val)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("%s must be an absolute https URL", name)
		}
	}
	return nil
}
