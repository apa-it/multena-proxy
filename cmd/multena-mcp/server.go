package main

import (
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const (
	mcpPath      = "/mcp"
	metadataPath = "/.well-known/oauth-protected-resource"
)

// newHandler builds the HTTP surface:
//
//	GET  /.well-known/oauth-protected-resource        RFC 9728 metadata (public)
//	GET  /.well-known/oauth-protected-resource/mcp    same document, path-suffixed variant
//	ANY  /mcp                                         MCP Streamable HTTP, bearer-protected
//	GET  /healthz                                     liveness
func newHandler(cfg config, tools *siTool, v *tokenVerifier) http.Handler {
	meta := &oauthex.ProtectedResourceMetadata{
		Resource:               cfg.ResourceURL(),
		AuthorizationServers:   []string{cfg.Issuer},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "multena-mcp",
	}
	prm := auth.ProtectedResourceMetadataHandler(meta)

	server := mcp.NewServer(&mcp.Implementation{Name: "multena-mcp", Version: version}, nil)
	tools.register(server)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true})

	guarded := auth.RequireBearerToken(v.verify, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: cfg.MetadataURL(),
	})(mcpHandler)

	mux := http.NewServeMux()
	mux.Handle(metadataPath, prm)
	mux.Handle(metadataPath+mcpPath, prm)
	mux.Handle(mcpPath, guarded)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}
