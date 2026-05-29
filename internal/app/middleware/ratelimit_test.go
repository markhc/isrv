package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
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

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// newMiddleware creates a fresh RateLimit middleware for each test, ensuring
// complete state isolation without any global resets.
func newMiddleware(t *testing.T, c models.RateLimitConfiguration) http.Handler {
	t.Helper()
	return RateLimit(t.Context(), c)(okHandler())
}

func req(ip string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = ip + ":1234"
	return r
}

func reqXFF(remoteIP, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteIP + ":1234"
	r.Header.Set("X-Forwarded-For", xff)
	return r
}

func do(h http.Handler, r *http.Request) int {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	return rr.Code
}

// ---------------------------------------------------------------------------
// Disabled / pass-through
// ---------------------------------------------------------------------------

func TestRateLimit_Disabled(t *testing.T) {
	h := newMiddleware(t, models.RateLimitConfiguration{Enabled: false})
	assert.Equal(t, http.StatusOK, do(h, req("1.2.3.4")))
}

func TestRateLimit_ZeroRPM(t *testing.T) {
	h := newMiddleware(t, models.RateLimitConfiguration{Enabled: true, RequestsPerMinute: 0})
	assert.Equal(t, http.StatusOK, do(h, req("1.2.3.4")))
}

// ---------------------------------------------------------------------------
// Basic allow / throttle / block
// ---------------------------------------------------------------------------

func TestRateLimit_AllowsWithinBurst(t *testing.T) {
	h := newMiddleware(t, cfg(600, 5, models.RateLimitActionThrottle))
	for range 5 {
		assert.Equal(t, http.StatusOK, do(h, req("10.0.0.1")))
	}
}

func TestRateLimit_ThrottleAfterBurst(t *testing.T) {
	// burst=2: first two requests pass, third gets 429
	h := newMiddleware(t, cfg(60, 2, models.RateLimitActionThrottle))
	do(h, req("10.0.0.2"))
	do(h, req("10.0.0.2"))
	assert.Equal(t, http.StatusTooManyRequests, do(h, req("10.0.0.2")))
}

func TestRateLimit_BlockAfterBurst(t *testing.T) {
	h := newMiddleware(t, cfg(60, 1, models.RateLimitActionBlock))
	do(h, req("10.0.0.3")) // consumes burst
	// second request exceeds limit → IP blocked → 403
	assert.Equal(t, http.StatusForbidden, do(h, req("10.0.0.3")))
	// subsequent requests remain blocked
	assert.Equal(t, http.StatusForbidden, do(h, req("10.0.0.3")))
}

func TestRateLimit_NoneActionAllows(t *testing.T) {
	h := newMiddleware(t, cfg(60, 1, models.RateLimitActionNone))
	do(h, req("10.0.0.6"))
	// burst exhausted but action=none → request still served
	assert.Equal(t, http.StatusOK, do(h, req("10.0.0.6")))
}

func TestRateLimit_UnrecognizedActionRejects(t *testing.T) {
	h := newMiddleware(t, cfg(60, 1, models.RateLimitExceededAction("unknown")))
	do(h, req("10.0.0.7"))
	assert.Equal(t, http.StatusTooManyRequests, do(h, req("10.0.0.7")))
}

// ---------------------------------------------------------------------------
// Burst boundary
// ---------------------------------------------------------------------------

func TestRateLimit_BurstExactBoundary(t *testing.T) {
	const burst = 50
	h := newMiddleware(t, cfg(6000, burst, models.RateLimitActionThrottle))
	for i := range burst {
		assert.Equal(t, http.StatusOK, do(h, req("20.0.0.1")), "request %d should be allowed", i+1)
	}
	assert.Equal(t, http.StatusTooManyRequests, do(h, req("20.0.0.1")), "request burst+1 should be rejected")
}

// ---------------------------------------------------------------------------
// Per-IP isolation
// ---------------------------------------------------------------------------

func TestRateLimit_IsolatedPerIP(t *testing.T) {
	h := newMiddleware(t, cfg(60, 1, models.RateLimitActionThrottle))
	do(h, req("10.1.0.1"))
	assert.Equal(t, http.StatusTooManyRequests, do(h, req("10.1.0.1")), "IP A should be throttled")
	assert.Equal(t, http.StatusOK, do(h, req("10.1.0.2")), "IP B should have its own fresh bucket")
}

func TestRateLimit_ManyIPsAllAllowed(t *testing.T) {
	h := newMiddleware(t, cfg(60, 3, models.RateLimitActionThrottle))
	for i := range 500 {
		ip := fmt.Sprintf("172.16.%d.%d", i/256, i%256)
		assert.Equal(t, http.StatusOK, do(h, req(ip)), "first request from %s should be allowed", ip)
	}
}

// ---------------------------------------------------------------------------
// Sustained flood
// ---------------------------------------------------------------------------

func TestRateLimit_SustainedFloodThrottled(t *testing.T) {
	const total, burst = 200, 5
	h := newMiddleware(t, cfg(60, burst, models.RateLimitActionThrottle))
	allowed := 0
	for range total {
		if do(h, req("30.0.0.1")) == http.StatusOK {
			allowed++
		}
	}
	assert.Equal(t, burst, allowed, "only burst-size requests should be allowed")
}

func TestRateLimit_SustainedFloodBlocked(t *testing.T) {
	const total, burst = 200, 5
	h := newMiddleware(t, cfg(60, burst, models.RateLimitActionBlock))
	statuses := make([]int, total)
	for i := range total {
		statuses[i] = do(h, req("30.0.0.2"))
	}
	for i := range burst {
		assert.Equal(t, http.StatusOK, statuses[i], "request %d should be allowed", i+1)
	}
	for i := burst; i < total; i++ {
		assert.Equal(t, http.StatusForbidden, statuses[i], "request %d should be blocked", i+1)
	}
}

func TestRateLimit_AggressiveIPBlockedWhileOtherServed(t *testing.T) {
	h := newMiddleware(t, cfg(60, 3, models.RateLimitActionBlock))
	for range 3 + 5 {
		do(h, req("40.0.0.1"))
	}
	assert.Equal(t, http.StatusForbidden, do(h, req("40.0.0.1")), "aggressive IP should be blocked")
	assert.Equal(t, http.StatusOK, do(h, req("40.0.0.2")), "well-behaved IP should be unaffected")
}

// ---------------------------------------------------------------------------
// Whitelist
// ---------------------------------------------------------------------------

func TestRateLimit_WhitelistedIPBypass(t *testing.T) {
	c := cfg(1, 1, models.RateLimitActionThrottle)
	c.WhitelistIPs = []string{"192.168.1.1"}
	h := newMiddleware(t, c)
	for range 10 {
		assert.Equal(t, http.StatusOK, do(h, req("192.168.1.1")))
	}
}

func TestRateLimit_MultipleWhitelistedIPs(t *testing.T) {
	c := cfg(1, 1, models.RateLimitActionThrottle)
	c.WhitelistIPs = []string{"192.168.2.1", "192.168.2.2"}
	h := newMiddleware(t, c)
	for range 10 {
		assert.Equal(t, http.StatusOK, do(h, req("192.168.2.1")))
		assert.Equal(t, http.StatusOK, do(h, req("192.168.2.2")))
	}
}

func TestRateLimit_WhitelistAmongHighTraffic(t *testing.T) {
	c := cfg(60, 2, models.RateLimitActionBlock)
	c.WhitelistIPs = []string{"80.0.0.1"}
	h := newMiddleware(t, c)
	for i := range 20 {
		do(h, req(fmt.Sprintf("80.0.1.%d", i%5)))
	}
	for range 50 {
		assert.Equal(t, http.StatusOK, do(h, req("80.0.0.1")))
	}
}

// ---------------------------------------------------------------------------
// Trusted proxies / XFF
// ---------------------------------------------------------------------------

func TestRateLimit_XFFTrustedProxy(t *testing.T) {
	c := cfg(60, 1, models.RateLimitActionThrottle)
	c.TrustedProxies = []string{"10.10.0.0/24"}
	h := newMiddleware(t, c)

	// client 1.2.3.4 via trusted proxy — uses burst
	assert.Equal(t, http.StatusOK, do(h, reqXFF("10.10.0.1", "1.2.3.4")))
	// same client via another trusted proxy — bucket exhausted
	assert.Equal(t, http.StatusTooManyRequests, do(h, reqXFF("10.10.0.2", "1.2.3.4")))
	// different client via trusted proxy — own fresh bucket
	assert.Equal(t, http.StatusOK, do(h, reqXFF("10.10.0.1", "5.6.7.8")))
}

func TestRateLimit_XFFIgnoredWithoutTrustedProxies(t *testing.T) {
	c := cfg(60, 1, models.RateLimitActionThrottle)
	// no TrustedProxies → XFF ignored, keyed on RemoteAddr
	h := newMiddleware(t, c)
	assert.Equal(t, http.StatusOK, do(h, reqXFF("10.10.0.1", "1.2.3.4")))
	// different RemoteAddr with same XFF → separate bucket (XFF not trusted)
	assert.Equal(t, http.StatusOK, do(h, reqXFF("10.10.0.2", "1.2.3.4")))
	// same RemoteAddr again → bucket exhausted
	assert.Equal(t, http.StatusTooManyRequests, do(h, reqXFF("10.10.0.1", "9.9.9.9")))
}

func TestRateLimit_XFFSpoofingFromUntrustedSource(t *testing.T) {
	c := cfg(60, 1, models.RateLimitActionThrottle)
	c.TrustedProxies = []string{"10.0.0.1"}
	h := newMiddleware(t, c)

	do(h, reqXFF("99.99.99.99", "1.1.1.1"))
	// second request with different spoofed XFF, same untrusted RemoteAddr → throttled
	assert.Equal(t, http.StatusTooManyRequests, do(h, reqXFF("99.99.99.99", "2.2.2.2")),
		"spoofed XFF from untrusted source must not bypass rate limiting")
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
	h := newMiddleware(t, c)

	do(h, req("101.0.0.1")) // consume burst
	do(h, req("101.0.0.1")) // triggers block #1

	// wait for the first block to expire
	time.Sleep(base + 20*time.Millisecond)

	// limiter still has no tokens; next request exceeds limit → block #2 (longer)
	assert.Equal(t, http.StatusForbidden, do(h, req("101.0.0.1")))
}

// ---------------------------------------------------------------------------
// Concurrent access
// ---------------------------------------------------------------------------

func TestRateLimit_ConcurrentSingleIP(t *testing.T) {
	const goroutines, burst = 100, 10
	h := newMiddleware(t, cfg(6000, burst, models.RateLimitActionThrottle))

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			if do(h, req("50.0.0.1")) == http.StatusOK {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()

	assert.LessOrEqual(t, allowed.Load(), int64(burst),
		"concurrent allowed count must not exceed burst size")
}

func TestRateLimit_ConcurrentManyIPs(t *testing.T) {
	const ipCount = 100
	h := newMiddleware(t, cfg(60, 1, models.RateLimitActionThrottle))

	var failed atomic.Int64
	var wg sync.WaitGroup
	for i := range ipCount {
		ip := fmt.Sprintf("60.0.%d.1", i)
		wg.Go(func() {
			if do(h, req(ip)) != http.StatusOK {
				failed.Add(1)
			}
		})
	}
	wg.Wait()

	assert.Equal(t, int64(0), failed.Load(),
		"each IP's first request should succeed regardless of concurrency")
}

// ---------------------------------------------------------------------------
// Token bucket recovery
// ---------------------------------------------------------------------------

func TestRateLimit_Recovery(t *testing.T) {
	// 120 RPM = 2 tokens/sec, burst=1
	h := newMiddleware(t, cfg(120, 1, models.RateLimitActionThrottle))
	do(h, req("70.0.0.1"))
	assert.Equal(t, http.StatusTooManyRequests, do(h, req("70.0.0.1")), "should be throttled after burst")

	// wait for one token to refill (0.5 s at 120 RPM)
	time.Sleep(600 * time.Millisecond)
	assert.Equal(t, http.StatusOK, do(h, req("70.0.0.1")), "should recover after token refill")
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
