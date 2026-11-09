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

	// The REST API, MCP and the web pages. The API and the pages change
	// repositories on behalf of whoever the request is from, and in a
	// browser that can be decided by a cookie Outpost's gateway reads, so
	// changes from other sites are refused. MCP has its own protection.
	cop := crossOriginProtection(ctx)
	router.Path("/mcp").Handler(mcpHandler(v))
	router.PathPrefix("/v1/").Handler(cop.Handler(apiHandler{verifier: v}))
	router.PathPrefix("/").Handler(cop.Handler(uiHandler{verifier: v}))

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

// crossOriginProtection refuses requests that change something when a
// browser says they come from another site. Origins the operator allows for
// CORS are trusted to make them; a wildcard is not, since that would trust
// every site.
func crossOriginProtection(ctx context.Context) *http.CrossOriginProtection {
	cop := http.NewCrossOriginProtection()
	cfg := config.FromContext(ctx)
	for _, o := range cfg.HTTP.CORS.AllowedOrigins {
		if o == "" || o == "*" {
			continue
		}
		if err := cop.AddTrustedOrigin(o); err != nil {
			log.FromContext(ctx).Warn("ignoring CORS origin for cross-origin protection", "origin", o, "err", err)
		}
	}
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAPIError(w, http.StatusForbidden, "cross-origin request refused")
	}))
	return cop
}
