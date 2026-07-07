package middleware

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestMain(m *testing.M) {
	logging.InitializeNop()
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func cfg(rpm, burst int, action models.RateLimitExceededAction) models.RateLimitConfiguration {
	return models.RateLimitConfiguration{
		Enabled:           true,
		RequestsPerMinute: rpm,
		BurstSize:         burst,
		OnLimitExceeded:   action,
		BlockDuration:     5 * time.Minute,
	}
}

// newApp creates a Fiber app with RateLimit middleware applied and a 200 OK route.
// X-Test-IP is used as the effective remote IP so tests can control it via a header.
func newApp(t *testing.T, c models.RateLimitConfiguration) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{
		ProxyHeader: "X-Test-IP",
		TrustProxy:  true,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Proxies: []string{"0.0.0.0/0"},
		},
	})
	app.Use(RateLimit(t.Context(), c))
	app.Get("/", func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	return app
}

func req(ip string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Test-IP", ip)
	return r
}

func do(app *fiber.App, r *http.Request) int {
	resp, err := app.Test(r, fiber.TestConfig{Timeout: 5 * time.Second, FailOnTimeout: true})
	if err != nil {
		return -1
	}
	resp.Body.Close()
	return resp.StatusCode
}

// ---------------------------------------------------------------------------
// Disabled / pass-through
// ---------------------------------------------------------------------------

func TestRateLimit_Disabled(t *testing.T) {
	app := newApp(t, models.RateLimitConfiguration{Enabled: false})
	assert.Equal(t, http.StatusOK, do(app, req("1.2.3.4")))
}

func TestRateLimit_ZeroRPM(t *testing.T) {
	app := newApp(t, models.RateLimitConfiguration{Enabled: true, RequestsPerMinute: 0})
	assert.Equal(t, http.StatusOK, do(app, req("1.2.3.4")))
}

// ---------------------------------------------------------------------------
// Basic allow / throttle / block
// ---------------------------------------------------------------------------

func TestRateLimit_AllowsWithinBurst(t *testing.T) {
	app := newApp(t, cfg(600, 5, models.RateLimitActionThrottle))
	for range 5 {
		assert.Equal(t, http.StatusOK, do(app, req("10.0.0.1")))
	}
}

func TestRateLimit_ThrottleAfterBurst(t *testing.T) {
	// burst=2: first two requests pass, third gets 429
	app := newApp(t, cfg(60, 2, models.RateLimitActionThrottle))
	do(app, req("10.0.0.2"))
	do(app, req("10.0.0.2"))
	assert.Equal(t, http.StatusTooManyRequests, do(app, req("10.0.0.2")))
}

func TestRateLimit_BlockAfterBurst(t *testing.T) {
	app := newApp(t, cfg(60, 1, models.RateLimitActionBlock))
	do(app, req("10.0.0.3")) // consumes burst
	// second request exceeds limit → IP blocked → 403
	assert.Equal(t, http.StatusForbidden, do(app, req("10.0.0.3")))
	// subsequent requests remain blocked
	assert.Equal(t, http.StatusForbidden, do(app, req("10.0.0.3")))
}

func TestRateLimit_NoneActionAllows(t *testing.T) {
	app := newApp(t, cfg(60, 1, models.RateLimitActionNone))
	do(app, req("10.0.0.6"))
	// burst exhausted but action=none → request still served
	assert.Equal(t, http.StatusOK, do(app, req("10.0.0.6")))
}

func TestRateLimit_UnrecognizedActionRejects(t *testing.T) {
	app := newApp(t, cfg(60, 1, models.RateLimitExceededAction("unknown")))
	do(app, req("10.0.0.7"))
	assert.Equal(t, http.StatusTooManyRequests, do(app, req("10.0.0.7")))
}

// ---------------------------------------------------------------------------
// Burst boundary
// ---------------------------------------------------------------------------

func TestRateLimit_BurstExactBoundary(t *testing.T) {
	const burst = 50
	app := newApp(t, cfg(6000, burst, models.RateLimitActionThrottle))
	for i := range burst {
		assert.Equal(t, http.StatusOK, do(app, req("20.0.0.1")), "request %d should be allowed", i+1)
	}
	assert.Equal(t, http.StatusTooManyRequests, do(app, req("20.0.0.1")), "request burst+1 should be rejected")
}

// ---------------------------------------------------------------------------
// Per-IP isolation
// ---------------------------------------------------------------------------

func TestRateLimit_IsolatedPerIP(t *testing.T) {
	app := newApp(t, cfg(60, 1, models.RateLimitActionThrottle))
	do(app, req("10.1.0.1"))
	assert.Equal(t, http.StatusTooManyRequests, do(app, req("10.1.0.1")), "IP A should be throttled")
	assert.Equal(t, http.StatusOK, do(app, req("10.1.0.2")), "IP B should have its own fresh bucket")
}

func TestRateLimit_ManyIPsAllAllowed(t *testing.T) {
	app := newApp(t, cfg(60, 3, models.RateLimitActionThrottle))
	for i := range 500 {
		ip := fmt.Sprintf("172.16.%d.%d", i/256, i%256)
		assert.Equal(t, http.StatusOK, do(app, req(ip)), "first request from %s should be allowed", ip)
	}
}

// ---------------------------------------------------------------------------
// Sustained flood
// ---------------------------------------------------------------------------

func TestRateLimit_SustainedFloodThrottled(t *testing.T) {
	const total, burst = 200, 5
	app := newApp(t, cfg(60, burst, models.RateLimitActionThrottle))
	allowed := 0
	for range total {
		if do(app, req("30.0.0.1")) == http.StatusOK {
			allowed++
		}
	}
	assert.Equal(t, burst, allowed, "only burst-size requests should be allowed")
}

func TestRateLimit_SustainedFloodBlocked(t *testing.T) {
	const total, burst = 200, 5
	app := newApp(t, cfg(60, burst, models.RateLimitActionBlock))
	statuses := make([]int, total)
	for i := range total {
		statuses[i] = do(app, req("30.0.0.2"))
	}
	for i := range burst {
		assert.Equal(t, http.StatusOK, statuses[i], "request %d should be allowed", i+1)
	}
	for i := burst; i < total; i++ {
		assert.Equal(t, http.StatusForbidden, statuses[i], "request %d should be blocked", i+1)
	}
}

func TestRateLimit_AggressiveIPBlockedWhileOtherServed(t *testing.T) {
	app := newApp(t, cfg(60, 3, models.RateLimitActionBlock))
	for range 3 + 5 {
		do(app, req("40.0.0.1"))
	}
	assert.Equal(t, http.StatusForbidden, do(app, req("40.0.0.1")), "aggressive IP should be blocked")
	assert.Equal(t, http.StatusOK, do(app, req("40.0.0.2")), "well-behaved IP should be unaffected")
}

// ---------------------------------------------------------------------------
// Whitelist
// ---------------------------------------------------------------------------

func TestRateLimit_WhitelistedIPBypass(t *testing.T) {
	c := cfg(1, 1, models.RateLimitActionThrottle)
	c.WhitelistIPs = []string{"192.168.1.1"}
	app := newApp(t, c)
	for range 10 {
		assert.Equal(t, http.StatusOK, do(app, req("192.168.1.1")))
	}
}

func TestRateLimit_MultipleWhitelistedIPs(t *testing.T) {
	c := cfg(1, 1, models.RateLimitActionThrottle)
	c.WhitelistIPs = []string{"192.168.2.1", "192.168.2.2"}
	app := newApp(t, c)
	for range 10 {
		assert.Equal(t, http.StatusOK, do(app, req("192.168.2.1")))
		assert.Equal(t, http.StatusOK, do(app, req("192.168.2.2")))
	}
}

func TestRateLimit_WhitelistAmongHighTraffic(t *testing.T) {
	c := cfg(60, 2, models.RateLimitActionBlock)
	c.WhitelistIPs = []string{"80.0.0.1"}
	app := newApp(t, c)
	for i := range 20 {
		do(app, req(fmt.Sprintf("80.0.1.%d", i%5)))
	}
	for range 50 {
		assert.Equal(t, http.StatusOK, do(app, req("80.0.0.1")))
	}
}

// ---------------------------------------------------------------------------
// Exponential block backoff
// ---------------------------------------------------------------------------

func TestRateLimit_ExponentialBackoff(t *testing.T) {
	// Use a very short base duration so we can inspect relative durations without sleeping.
	const base = 100 * time.Millisecond
	c := cfg(60, 1, models.RateLimitActionBlock)
	c.BlockDuration = base

	rl := newRateLimiter(t.Context(), c)

	var prevUntil time.Time
	for offense := range 5 {
		rl.blockIP("1.2.3.4", base)

		rl.blockMu.Lock()
		entry := rl.blockList["1.2.3.4"]
		rl.blockMu.Unlock()

		assert.Equal(t, offense+1, entry.offenses, "offense count should be %d", offense+1)
		if offense > 0 {
			assert.True(t, entry.until.After(prevUntil),
				"offense %d block should expire later than offense %d", offense+1, offense)
		}
		prevUntil = entry.until
	}
}

func TestRateLimit_BackoffCappedAtMax(t *testing.T) {
	const base = time.Millisecond
	rl := newRateLimiter(t.Context(), cfg(60, 1, models.RateLimitActionBlock))

	for range maxBackoffFactor + 10 {
		rl.blockIP("2.3.4.5", base)
	}

	rl.blockMu.Lock()
	entry := rl.blockList["2.3.4.5"]
	rl.blockMu.Unlock()

	maxDuration := base * time.Duration(1<<maxBackoffFactor)
	assert.LessOrEqual(t, time.Until(entry.until), maxDuration+50*time.Millisecond,
		"block duration must not exceed the cap of %v", maxDuration)
}

func TestRateLimit_BlockReappliedAfterExpiry(t *testing.T) {
	const base = 50 * time.Millisecond
	c := cfg(60, 1, models.RateLimitActionBlock)
	c.BlockDuration = base
	app := newApp(t, c)

	do(app, req("101.0.0.1")) // consume burst
	do(app, req("101.0.0.1")) // triggers block #1

	// wait for the first block to expire
	time.Sleep(base + 20*time.Millisecond)

	// limiter still has no tokens; next request exceeds limit → block #2 (longer)
	assert.Equal(t, http.StatusForbidden, do(app, req("101.0.0.1")))
}

// ---------------------------------------------------------------------------
// Concurrent access
//
// app.Test uses a shared in-memory connection that races on fasthttp's
// header pool when many goroutines call it concurrently. For tests where
// many goroutines must hit distinct IPs, spin up a real TCP listener so
// each call has its own connection.
// ---------------------------------------------------------------------------

func newAppOnListener(t *testing.T, c models.RateLimitConfiguration) (string, func()) {
	t.Helper()
	app := newApp(t, c)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		_ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	}()

	// Wait for the server to be reachable.
	addr := ln.Addr().String()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.Dial("tcp", addr)
		if dialErr == nil {
			conn.Close()
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	cleanup := func() {
		_ = app.Shutdown()
	}
	return addr, cleanup
}

func httpGet(client *http.Client, addr, ip string) int {
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	if err != nil {
		return -1
	}
	req.Header.Set("X-Test-IP", ip)
	resp, err := client.Do(req)
	if err != nil {
		return -1
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// func TestRateLimit_ConcurrentSingleIP(t *testing.T) {
// 	const goroutines, burst = 100, 10
// 	addr, cleanup := newAppOnListener(t, cfg(6000, burst, models.RateLimitActionThrottle))
// 	defer cleanup()

// 	client := &http.Client{Timeout: 5 * time.Second}

// 	var allowed atomic.Int64
// 	var wg sync.WaitGroup
// 	for range goroutines {
// 		wg.Go(func() {
// 			if httpGet(client, addr, "50.0.0.1") == http.StatusOK {
// 				allowed.Add(1)
// 			}
// 		})
// 	}
// 	wg.Wait()

// 	assert.LessOrEqual(t, allowed.Load(), int64(burst),
// 		"concurrent allowed count must not exceed burst size")
// }

// func TestRateLimit_ConcurrentManyIPs(t *testing.T) {
// 	const ipCount = 100
// 	addr, cleanup := newAppOnListener(t, cfg(60, 1, models.RateLimitActionThrottle))
// 	defer cleanup()

// 	client := &http.Client{Timeout: 5 * time.Second}

// 	var failed atomic.Int64
// 	var wg sync.WaitGroup
// 	for i := range ipCount {
// 		ip := fmt.Sprintf("60.0.%d.1", i)
// 		wg.Go(func() {
// 			if httpGet(client, addr, ip) != http.StatusOK {
// 				failed.Add(1)
// 			}
// 		})
// 	}
// 	wg.Wait()

// 	assert.Equal(t, int64(0), failed.Load(),
// 		"each IP's first request should succeed regardless of concurrency")
// }

// ---------------------------------------------------------------------------
// Token bucket recovery
// ---------------------------------------------------------------------------

func TestRateLimit_Recovery(t *testing.T) {
	// 120 RPM = 2 tokens/sec, burst=1
	app := newApp(t, cfg(120, 1, models.RateLimitActionThrottle))
	do(app, req("70.0.0.1"))
	assert.Equal(t, http.StatusTooManyRequests, do(app, req("70.0.0.1")), "should be throttled after burst")

	// wait for one token to refill (0.5 s at 120 RPM)
	time.Sleep(600 * time.Millisecond)
	assert.Equal(t, http.StatusOK, do(app, req("70.0.0.1")), "should recover after token refill")
}

// ---------------------------------------------------------------------------
// Cleanup
// ---------------------------------------------------------------------------

func TestRateLimit_CleanupRemovesExpiredVisitors(t *testing.T) {
	rl := newRateLimiter(t.Context(), cfg(60, 5, models.RateLimitActionThrottle))

	// inject a visitor with a lastSeen well in the past
	rl.visitorsMu.Lock()
	rl.visitors["old.ip"] = &visitorEntry{
		limiter:  rate.NewLimiter(1, 1),
		lastSeen: time.Now().Add(-(visitorTTL + time.Minute)),
	}
	rl.visitors["recent.ip"] = &visitorEntry{
		limiter:  rate.NewLimiter(1, 1),
		lastSeen: time.Now(),
	}
	rl.visitorsMu.Unlock()

	rl.cleanup()

	rl.visitorsMu.Lock()
	_, oldExists := rl.visitors["old.ip"]
	_, recentExists := rl.visitors["recent.ip"]
	rl.visitorsMu.Unlock()

	assert.False(t, oldExists, "expired visitor should be removed")
	assert.True(t, recentExists, "recent visitor should be kept")
}

func TestRateLimit_CleanupRemovesExpiredBlocks(t *testing.T) {
	rl := newRateLimiter(t.Context(), cfg(60, 5, models.RateLimitActionBlock))

	rl.blockMu.Lock()
	rl.blockList["expired.ip"] = blockEntry{until: time.Now().Add(-time.Minute), offenses: 1}
	rl.blockList["active.ip"] = blockEntry{until: time.Now().Add(time.Minute), offenses: 1}
	rl.blockMu.Unlock()

	rl.cleanup()

	rl.blockMu.Lock()
	_, expiredExists := rl.blockList["expired.ip"]
	_, activeExists := rl.blockList["active.ip"]
	rl.blockMu.Unlock()

	assert.False(t, expiredExists, "expired block should be removed")
	assert.True(t, activeExists, "active block should be kept")
}

// ---------------------------------------------------------------------------
// RateLimitFailedLogins
//
// Uses a hardcoded strict config (burst 1, block on exceed). The downstream
// handler returns 401 when the request carries X-Login=fail, else 200. Only
// failed (401) responses spend rate-limit budget.
// ---------------------------------------------------------------------------

// newLoginApp builds a Fiber app guarded by RateLimitFailedLogins.
func newLoginApp(t *testing.T) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{
		ProxyHeader: "X-Test-IP",
		TrustProxy:  true,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Proxies: []string{"0.0.0.0/0"},
		},
	})
	cfg := models.AdminConfiguration{
		FailedLoginLimit:         5,
		FailedLoginBlockDuration: 12 * time.Hour,
	}
	app.Use(RateLimitFailedLogins(t.Context(), cfg))
	app.Post("/login", func(c fiber.Ctx) error {
		if c.Get("X-Login") == "fail" {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		return c.SendStatus(fiber.StatusOK)
	})
	return app
}

// loginReq builds a POST /login request for the given IP. When fail is true the
// downstream handler is instructed to respond 401.
func loginReq(ip string, fail bool) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/login", nil)
	r.Header.Set("X-Test-IP", ip)
	if fail {
		r.Header.Set("X-Login", "fail")
	}
	return r
}

func TestRateLimitFailedLogins_SuccessesNeverBlocked(t *testing.T) {
	app := newLoginApp(t)
	for range 50 {
		assert.Equal(t, http.StatusOK, do(app, loginReq("11.0.0.1", false)),
			"successful logins must never be throttled")
	}
}

func TestRateLimitFailedLogins_FirstFailureAllowed(t *testing.T) {
	app := newLoginApp(t)
	// The full hourly budget is available up front, so a failed attempt passes through.
	assert.Equal(t, http.StatusUnauthorized, do(app, loginReq("11.0.0.2", true)))
}

func TestRateLimitFailedLogins_RepeatedFailuresBlocked(t *testing.T) {
	app := newLoginApp(t)
	maxFailedLoginsPerHour := 5

	// The hourly budget of failed attempts each return the handler's 401.
	for i := range maxFailedLoginsPerHour {
		assert.Equal(t, http.StatusUnauthorized, do(app, loginReq("11.0.0.3", true)),
			"failed attempt %d should be within budget", i+1)
	}
	// The next failure exhausts the budget and blocks the IP.
	assert.Equal(t, http.StatusForbidden, do(app, loginReq("11.0.0.3", true)))
	// Once blocked, even valid credentials are rejected.
	assert.Equal(t, http.StatusForbidden, do(app, loginReq("11.0.0.3", false)))
}

func TestRateLimitFailedLogins_SuccessesDoNotConsumeBudget(t *testing.T) {
	app := newLoginApp(t)
	maxFailedLoginsPerHour := 5

	for range 20 {
		assert.Equal(t, http.StatusOK, do(app, loginReq("11.0.0.4", false)))
	}
	// Budget is untouched by successes: the full hourly budget of failures is still allowed.
	for i := range maxFailedLoginsPerHour {
		assert.Equal(t, http.StatusUnauthorized, do(app, loginReq("11.0.0.4", true)),
			"failed attempt %d should be within budget", i+1)
	}
	// The next failure trips the block.
	assert.Equal(t, http.StatusForbidden, do(app, loginReq("11.0.0.4", true)))
}

func TestRateLimitFailedLogins_IsolatedPerIP(t *testing.T) {
	app := newLoginApp(t)
	maxFailedLoginsPerHour := 5

	// Exhaust IP A's budget and block it.
	for range maxFailedLoginsPerHour {
		do(app, loginReq("11.1.0.1", true))
	}
	assert.Equal(t, http.StatusForbidden, do(app, loginReq("11.1.0.1", true)))
	// IP B has its own fresh budget.
	assert.Equal(t, http.StatusUnauthorized, do(app, loginReq("11.1.0.2", true)))
	assert.Equal(t, http.StatusOK, do(app, loginReq("11.1.0.2", false)))
}

func TestRateLimitFailedLogins_BlockedRequestSkipsHandler(t *testing.T) {
	var calls atomic.Int64
	app := fiber.New(fiber.Config{
		ProxyHeader: "X-Test-IP",
		TrustProxy:  true,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Proxies: []string{"0.0.0.0/0"},
		},
	})
	cfg := models.AdminConfiguration{
		FailedLoginLimit:         5,
		FailedLoginBlockDuration: 12 * time.Hour,
	}

	app.Use(RateLimitFailedLogins(t.Context(), cfg))
	app.Post("/login", func(c fiber.Ctx) error {
		calls.Add(1)
		return c.SendStatus(fiber.StatusUnauthorized)
	})

	// Exhaust the budget and trip the block; the handler runs for each of these.
	for range cfg.FailedLoginLimit + 1 {
		do(app, loginReq("11.2.0.1", true))
	}
	callsBeforeBlock := calls.Load()

	assert.Equal(t, http.StatusForbidden, do(app, loginReq("11.2.0.1", true)))
	assert.Equal(t, callsBeforeBlock, calls.Load(),
		"handler must not run once the IP is blocked")
}

func TestRateLimitFailedLogins_HandlerErrorPropagates(t *testing.T) {
	app := fiber.New(fiber.Config{
		ProxyHeader: "X-Test-IP",
		TrustProxy:  true,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Proxies: []string{"0.0.0.0/0"},
		},
	})
	cfg := models.AdminConfiguration{
		FailedLoginLimit:         5,
		FailedLoginBlockDuration: 12 * time.Hour,
	}
	app.Use(RateLimitFailedLogins(t.Context(), cfg))
	app.Post("/login", func(c fiber.Ctx) error {
		return fiber.NewError(fiber.StatusInternalServerError, "boom")
	})

	assert.Equal(t, http.StatusInternalServerError, do(app, loginReq("11.3.0.1", true)),
		"handler errors must propagate to Fiber's error handler")
}
