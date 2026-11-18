package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	grantTypeTokenExchange = "urn:ietf:params:oauth:grant-type:token-exchange"
	tokenTypeAccessToken   = "urn:ietf:params:oauth:token-type:access_token"
	maxTokenResponseBytes  = 64 << 10
)

// errTokenExchange is returned for every failed exchange. It deliberately carries
// no Keycloak payload, so neither tokens nor error details reach the MCP client.
var errTokenExchange = errors.New("token exchange failed")

// tokenExchanger performs an OAuth 2.0 Token Exchange (RFC 8693) at Keycloak:
// it trades the inbound user token (aud=multena-mcp) for a token with
// aud=multena, which is what multena-proxy accepts. The exchange client's
// Keycloak configuration (permission, audience mapper, admin-group removal)
// is documented in the PR, not enforced here.
type tokenExchanger struct {
	endpoint     string
	clientID     string
	clientSecret string
	audience     string
	http         *http.Client
}

func (e *tokenExchanger) Exchange(ctx context.Context, subjectToken string) (string, error) {
	form := url.Values{
		"grant_type":           {grantTypeTokenExchange},
		"subject_token":        {subjectToken},
		"subject_token_type":   {tokenTypeAccessToken},
		"requested_token_type": {tokenTypeAccessToken},
		"audience":             {e.audience},
		"client_id":            {e.clientID},
		"client_secret":        {e.clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errTokenExchange
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: transport", errTokenExchange)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: status %d", errTokenExchange, resp.StatusCode)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxTokenResponseBytes)).Decode(&out); err != nil {
		return "", fmt.Errorf("%w: decode", errTokenExchange)
	}
	if out.AccessToken == "" || !strings.EqualFold(out.TokenType, "bearer") {
		return "", fmt.Errorf("%w: no bearer token in response", errTokenExchange)
	}
	return out.AccessToken, nil
}
