// multena-mcp is a user-authenticated MCP endpoint in front of multena-proxy.
//
// It is an OAuth 2.0 resource server for MCP clients (Streamable HTTP). It is
// NOT a security boundary: multena-proxy remains the enforcement point. Incoming
// user tokens (aud=multena-mcp) are exchanged at Keycloak (RFC 8693) for a token
// with aud=multena before any call to the proxy. The user token is never passed
// through, and there is no service-account fallback.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "spike"

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Info().Str("version", version).Msg("multena-mcp starting")

	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid configuration")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	verifier, err := newTokenVerifier(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("cannot initialise token verifier")
	}

	tools := &siTool{
		tokens: &tokenExchanger{
			endpoint:     cfg.TokenEndpoint,
			clientID:     cfg.ClientID,
			clientSecret: cfg.ClientSecret,
			audience:     cfg.UpstreamAudience,
			http:         &http.Client{Timeout: 10 * time.Second},
		},
		proxyURL: cfg.ProxyURL,
		http:     &http.Client{Timeout: 30 * time.Second},
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           newHandler(cfg, tools, verifier),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info().Str("listen", cfg.ListenAddr).Str("resource", cfg.ResourceURL()).
		Str("issuer", cfg.Issuer).Str("audience", cfg.Audience).
		Str("upstream_audience", cfg.UpstreamAudience).Msg("multena-mcp ready")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal().Err(err).Msg("server stopped")
	}
}
