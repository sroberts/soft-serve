package web

import (
	"context"
	"net/http"

	"charm.land/log/v2"
	"github.com/charmbracelet/soft-serve/pkg/config"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
)

// NewRouter returns a new HTTP router. Without a usable Outpost gateway
// configuration, no Outpost identity is trusted; NewHTTPServer refuses to
// start in that case instead.
func NewRouter(ctx context.Context) http.Handler {
	v, err := newOutpostVerifier()
	if err != nil {
		log.FromContext(ctx).Error("outpost identity is not available", "err", err)
	}
	return newRouter(ctx, v)
}

func newRouter(ctx context.Context, v *outpostVerifier) http.Handler {
	logger := log.FromContext(ctx).WithPrefix("http")
	router := mux.NewRouter()

	// Health routes
	HealthController(ctx, router)

	// Git routes. They come first, so a clone, fetch or push is never
	// mistaken for a page.
	GitController(ctx, router)

	// The read-only REST API, MCP and the web pages.
	router.Path("/mcp").Handler(mcpHandler(v))
	router.PathPrefix("/v1/").Handler(apiHandler{verifier: v})
	router.PathPrefix("/").Handler(uiHandler{verifier: v})

	// Context handler
	// Adds context to the request
	h := NewLoggingMiddleware(router, logger)
	h = NewContextHandler(ctx)(h)
	h = handlers.RecoveryHandler()(h)

	cfg := config.FromContext(ctx)

	h = handlers.CORS(handlers.AllowedHeaders(cfg.HTTP.CORS.AllowedHeaders),
		handlers.AllowedOrigins(cfg.HTTP.CORS.AllowedOrigins),
		handlers.AllowedMethods(cfg.HTTP.CORS.AllowedMethods),
	)(h)

	return h
}
