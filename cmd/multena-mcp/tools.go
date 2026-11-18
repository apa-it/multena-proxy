package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
)

const siValuesPath = "/api/v1/label/si/values"

// listSIInput is empty: the tool takes no arguments.
type listSIInput struct{}

// listSIOutput is returned as structured content.
type listSIOutput struct {
	ServiceInstances []string `json:"service_instances"`
}

// upstreamTokenSource turns the inbound user token into the token used for the
// upstream call. Implemented by tokenExchanger; kept as an interface for tests.
type upstreamTokenSource interface {
	Exchange(ctx context.Context, subjectToken string) (string, error)
}

// siTool is the only tool of the spike. It asks multena-proxy which `si` values
// the calling user may use. The proxy stays the enforcement point; this tool
// performs no filtering of its own and has no service-account fallback.
type siTool struct {
	tokens   upstreamTokenSource
	proxyURL string
	http     *http.Client
}

func (t *siTool) handle(ctx context.Context, req *mcp.CallToolRequest, _ listSIInput) (*mcp.CallToolResult, listSIOutput, error) {
	var info *auth.TokenInfo
	if req != nil && req.Extra != nil {
		info = req.Extra.TokenInfo
	}
	if info == nil {
		return nil, listSIOutput{}, errors.New("no authenticated session")
	}
	inbound, _ := info.Extra[bearerInfoKey].(string)
	if inbound == "" {
		return nil, listSIOutput{}, errors.New("no authenticated session")
	}

	upstream, err := t.tokens.Exchange(ctx, inbound)
	if err != nil {
		log.Warn().Err(err).Str("sub", info.UserID).Msg("list_service_instances: token exchange failed")
		return nil, listSIOutput{}, errors.New("upstream authorization unavailable")
	}

	values, err := t.fetchSIValues(ctx, upstream)
	if err != nil {
		log.Warn().Err(err).Str("sub", info.UserID).Msg("list_service_instances: proxy call failed")
		return nil, listSIOutput{}, errors.New("multena-proxy request failed")
	}
	log.Info().Str("sub", info.UserID).Int("si_count", len(values)).Msg("list_service_instances")
	return nil, listSIOutput{ServiceInstances: values}, nil
}

// fetchSIValues calls GET /api/v1/label/si/values on multena-proxy with the
// exchanged bearer token and returns the Prometheus-style data array.
func (t *siTool) fetchSIValues(ctx context.Context, bearer string) ([]string, error) {
	endpoint, err := url.JoinPath(t.proxyURL, siValuesPath)
	if err != nil {
		return nil, fmt.Errorf("build proxy URL: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "application/json")

	resp, err := t.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("proxy returned status %d", resp.StatusCode)
	}

	var body struct {
		Status string   `json:"status"`
		Data   []string `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode proxy response: %w", err)
	}
	if body.Status != "success" {
		return nil, fmt.Errorf("proxy status %q", body.Status)
	}
	if body.Data == nil {
		body.Data = []string{}
	}
	return body.Data, nil
}

func (t *siTool) register(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_service_instances",
		Description: "Liefert die Service-Instance-Werte (si), auf die der eingeloggte Benutzer in multena-proxy zugreifen darf. Keine Eingabeparameter.",
	}, t.handle)
}
