package router

import (
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/webserver/middleware"
)

// RouteBuilder accumulates the configuration for a single route before it is
// registered with the underlying mux.Router via Router.Build.
type RouteBuilder struct {
	router  *Router
	path    string
	handler http.Handler
	methods []string
	mws     []func(http.Handler) http.Handler
}

// Methods restricts the route to the given HTTP methods.
func (rb *RouteBuilder) Methods(methods ...string) *RouteBuilder {
	rb.methods = append(rb.methods, methods...)

	return rb
}

// Use appends a middleware to the route's middleware chain.
// Middlewares are applied in the order they are added (outermost first).
func (rb *RouteBuilder) Use(mw func(http.Handler) http.Handler) *RouteBuilder {
	rb.mws = append(rb.mws, mw)

	return rb
}

// WithLogging wraps the handler with request-logging middleware.
func (rb *RouteBuilder) WithLogging() *RouteBuilder {
	trustedProxies := rb.router.config.TrustedProxies

	return rb.Use(func(next http.Handler) http.Handler {
		return middleware.WithRequestLogging(trustedProxies, next)
	})
}

// WithRateLimit wraps the handler with the rate-limiting middleware.
func (rb *RouteBuilder) WithRateLimit() *RouteBuilder {
	rateLimit := rb.router.config.RateLimit

	return rb.Use(func(next http.Handler) http.Handler {
		return middleware.WithRateLimit(rateLimit, next)
	})
}

// Router is a thin wrapper around *mux.Router that supports fluent route
// registration with middleware chaining. Call Build once all routes have
// been declared to register them with the underlying mux.
type Router struct {
	mux    *mux.Router
	config *models.Configuration
	routes []*RouteBuilder
}

// NewRouter creates a Router that writes routes into the provided mux.Router.
func NewRouter(m *mux.Router, config *models.Configuration) *Router {
	return &Router{mux: m, config: config}
}

// Handle begins a new route declaration for path and returns a *RouteBuilder
// so that methods and middleware can be attached via chaining.
func (r *Router) Handle(path string, h http.Handler) *RouteBuilder {
	rb := &RouteBuilder{
		router:  r,
		path:    path,
		handler: h,
	}
	r.routes = append(r.routes, rb)

	return rb
}

// Build registers all declared routes with the underlying mux.Router.
// Middleware is applied in declaration order (first declared = outermost wrapper).
func (r *Router) Build() error {
	for _, rb := range r.routes {
		if rb.methods == nil {
			return fmt.Errorf("no methods specified for route %q", rb.path)
		}

		h := rb.handler
		// Walk middlewares in reverse so the first declared wraps outermost.
		for i := len(rb.mws) - 1; i >= 0; i-- {
			h = rb.mws[i](h)
		}
		route := r.mux.Handle(rb.path, h)
		if len(rb.methods) > 0 {
			route.Methods(rb.methods...)
		}
	}

	return nil
}
