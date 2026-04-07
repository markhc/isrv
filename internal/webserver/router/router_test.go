package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gorilla/mux"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	logging.InitializeNop()
	os.Exit(m.Run())
}

// newTestConfig returns a minimal Configuration suitable for use in router tests.
func newTestConfig() *models.Configuration {
	return &models.Configuration{}
}

// okHandler is a simple handler that writes 200 OK.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
})

// --------------------------------------------------------------------------
// RouteBuilder.Methods
// --------------------------------------------------------------------------

func TestRouteBuilder_Methods_SingleCall(t *testing.T) {
	rb := &RouteBuilder{}
	result := rb.Methods("GET", "POST")

	assert.Equal(t, []string{"GET", "POST"}, rb.methods)
	assert.Same(t, rb, result, "Methods should return the same RouteBuilder for chaining")
}

func TestRouteBuilder_Methods_ChainedCalls(t *testing.T) {
	rb := &RouteBuilder{}
	rb.Methods("GET").Methods("POST").Methods("PUT")

	assert.Equal(t, []string{"GET", "POST", "PUT"}, rb.methods)
}

// --------------------------------------------------------------------------
// RouteBuilder.Use
// --------------------------------------------------------------------------

func TestRouteBuilder_Use_AccumulatesMiddlewares(t *testing.T) {
	noop := func(next http.Handler) http.Handler { return next }

	rb := &RouteBuilder{}
	result := rb.Use(noop).Use(noop).Use(noop)

	assert.Len(t, rb.mws, 3)
	assert.Same(t, rb, result, "Use should return the same RouteBuilder for chaining")
}

// --------------------------------------------------------------------------
// RouteBuilder.WithLogging
// --------------------------------------------------------------------------

func TestRouteBuilder_WithLogging_AddsMiddleware(t *testing.T) {
	cfg := newTestConfig()
	r := NewRouter(mux.NewRouter(), cfg)

	rb := r.Handle("/log", okHandler)
	before := len(rb.mws)
	rb.WithLogging()

	assert.Equal(t, before+1, len(rb.mws))
}

func TestRouteBuilder_WithLogging_ReturnsRouteBuilder(t *testing.T) {
	cfg := newTestConfig()
	r := NewRouter(mux.NewRouter(), cfg)

	rb := r.Handle("/log", okHandler)
	result := rb.WithLogging()

	assert.Same(t, rb, result)
}

// --------------------------------------------------------------------------
// RouteBuilder.WithRateLimit
// --------------------------------------------------------------------------

func TestRouteBuilder_WithRateLimit_AddsMiddleware(t *testing.T) {
	cfg := newTestConfig()
	r := NewRouter(mux.NewRouter(), cfg)

	rb := r.Handle("/rl", okHandler)
	before := len(rb.mws)
	rb.WithRateLimit()

	assert.Equal(t, before+1, len(rb.mws))
}

func TestRouteBuilder_WithRateLimit_DisabledPassesThrough(t *testing.T) {
	// When rate limiting is disabled the middleware should not block requests.
	cfg := &models.Configuration{
		RateLimit: models.RateLimitConfiguration{Enabled: false},
	}
	muxRouter := mux.NewRouter()
	r := NewRouter(muxRouter, cfg)
	r.Handle("/rl", okHandler).WithRateLimit().Methods("GET")
	require.NoError(t, r.Build())

	req := httptest.NewRequest("GET", "/rl", nil)
	w := httptest.NewRecorder()
	muxRouter.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// --------------------------------------------------------------------------
// Router.Handle
// --------------------------------------------------------------------------

func TestRouter_Handle_AppendsRoute(t *testing.T) {
	r := NewRouter(mux.NewRouter(), newTestConfig())

	assert.Empty(t, r.routes)
	r.Handle("/a", okHandler)
	assert.Len(t, r.routes, 1)
	r.Handle("/b", okHandler)
	assert.Len(t, r.routes, 2)
}

func TestRouter_Handle_StoresPathAndHandler(t *testing.T) {
	r := NewRouter(mux.NewRouter(), newTestConfig())
	rb := r.Handle("/test", okHandler)

	assert.Equal(t, "/test", rb.path)
	assert.NotNil(t, rb.handler)
}

func TestRouter_Handle_SetsParentRouter(t *testing.T) {
	r := NewRouter(mux.NewRouter(), newTestConfig())
	rb := r.Handle("/test", okHandler)

	assert.Same(t, r, rb.router)
}

// --------------------------------------------------------------------------
// Router.Build
// --------------------------------------------------------------------------

func TestRouter_Build_ErrorWhenNoMethods(t *testing.T) {
	r := NewRouter(mux.NewRouter(), newTestConfig())
	r.Handle("/no-methods", okHandler) // no .Methods(...)

	err := r.Build()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/no-methods")
}

func TestRouter_Build_ErrorReportsCorrectPath(t *testing.T) {
	r := NewRouter(mux.NewRouter(), newTestConfig())
	r.Handle("/ok", okHandler).Methods("GET")
	r.Handle("/missing", okHandler) // missing methods

	err := r.Build()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/missing")
}

func TestRouter_Build_NoError(t *testing.T) {
	r := NewRouter(mux.NewRouter(), newTestConfig())
	r.Handle("/", okHandler).Methods("GET")

	assert.NoError(t, r.Build())
}

func TestRouter_Build_HandlerRespondsCorrectly(t *testing.T) {
	muxRouter := mux.NewRouter()
	r := NewRouter(muxRouter, newTestConfig())
	r.Handle("/hello", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})).Methods("GET")
	require.NoError(t, r.Build())

	req := httptest.NewRequest("GET", "/hello", nil)
	w := httptest.NewRecorder()
	muxRouter.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestRouter_Build_WrongMethodReturns405(t *testing.T) {
	muxRouter := mux.NewRouter()
	r := NewRouter(muxRouter, newTestConfig())
	r.Handle("/only-get", okHandler).Methods("GET")
	require.NoError(t, r.Build())

	req := httptest.NewRequest("POST", "/only-get", nil)
	w := httptest.NewRecorder()
	muxRouter.ServeHTTP(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestRouter_Build_UnknownPathReturns404(t *testing.T) {
	muxRouter := mux.NewRouter()
	r := NewRouter(muxRouter, newTestConfig())
	r.Handle("/known", okHandler).Methods("GET")
	require.NoError(t, r.Build())

	req := httptest.NewRequest("GET", "/unknown", nil)
	w := httptest.NewRecorder()
	muxRouter.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRouter_Build_MultipleRoutes(t *testing.T) {
	muxRouter := mux.NewRouter()
	r := NewRouter(muxRouter, newTestConfig())

	r.Handle("/a", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).Methods("GET")
	r.Handle("/b", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).Methods("POST")
	require.NoError(t, r.Build())

	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{"GET", "/a", http.StatusOK},
		{"POST", "/b", http.StatusAccepted},
		{"GET", "/b", http.StatusMethodNotAllowed},
		{"POST", "/a", http.StatusMethodNotAllowed},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		w := httptest.NewRecorder()
		muxRouter.ServeHTTP(w, req)
		assert.Equalf(t, tc.want, w.Code, "%s %s", tc.method, tc.path)
	}
}

func TestRouter_Build_MultipleMethodsOnSameRoute(t *testing.T) {
	muxRouter := mux.NewRouter()
	r := NewRouter(muxRouter, newTestConfig())
	r.Handle("/multi", okHandler).Methods("GET", "POST")
	require.NoError(t, r.Build())

	for _, method := range []string{"GET", "POST"} {
		req := httptest.NewRequest(method, "/multi", nil)
		w := httptest.NewRecorder()
		muxRouter.ServeHTTP(w, req)
		assert.Equalf(t, http.StatusOK, w.Code, "method %s", method)
	}
}

// --------------------------------------------------------------------------
// Middleware ordering
// --------------------------------------------------------------------------

func TestRouter_MiddlewaresAreExecuted(t *testing.T) {
	var called []string

	mw1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = append(called, "mw1")
			next.ServeHTTP(w, r)
		})
	}

	mw2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = append(called, "mw2")
			next.ServeHTTP(w, r)
		})
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = append(called, "handler")
	})

	muxRouter := mux.NewRouter()
	r := NewRouter(muxRouter, nil)
	r.Handle("/", handler).Use(mw1).Use(mw2).Methods("GET")
	err := r.Build()
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)

	assert.Equal(t, []string{"mw1", "mw2", "handler"}, called)
}

func TestRouter_MiddlewareOrder_FirstDeclaredIsOutermost(t *testing.T) {
	// The first declared middleware must be the outermost wrapper, meaning it
	// runs first on the way in and last on the way out.
	var order []string

	wrap := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+":before")
				next.ServeHTTP(w, r)
				order = append(order, name+":after")
			})
		}
	}

	muxRouter := mux.NewRouter()
	r := NewRouter(muxRouter, nil)
	r.Handle("/order", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "handler")
	})).Use(wrap("A")).Use(wrap("B")).Methods("GET")
	require.NoError(t, r.Build())

	muxRouter.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/order", nil))

	assert.Equal(t, []string{"A:before", "B:before", "handler", "B:after", "A:after"}, order)
}

func TestRouter_NoMiddleware_HandlerCalledDirectly(t *testing.T) {
	var handlerCalled bool

	muxRouter := mux.NewRouter()
	r := NewRouter(muxRouter, newTestConfig())
	r.Handle("/direct", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handlerCalled = true
		w.WriteHeader(http.StatusOK)
	})).Methods("GET")
	require.NoError(t, r.Build())

	muxRouter.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/direct", nil))
	assert.True(t, handlerCalled)
}
