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

// blockInfo returns the block expiry time and offense count for ip.
// Both values are zero if the IP has no entry.
func blockInfo(ip string) (until time.Time, offenses int) {
	blockMu.Lock()
	defer blockMu.Unlock()

	e := blockList[ip]

	return e.until, e.offenses
}

// expireBlock backdates an IP's block so it appears already expired.
func expireBlock(ip string) {
	blockMu.Lock()
	defer blockMu.Unlock()

	e := blockList[ip]
	e.until = time.Now().Add(-time.Millisecond)
	blockList[ip] = e
}

// clearState clears all rate-limiter and block-list state.
func clearState() {
	visitorsMu.Lock()
	visitors = make(map[string]*rate.Limiter)
	visitorsMu.Unlock()

	blockMu.Lock()
	blockList = make(map[string]blockEntry)
	blockMu.Unlock()
}

func rateLimitConfig(rpm int, burst int, action models.RateLimitExceededAction) models.RateLimitConfiguration {
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

func doRequest(handler http.Handler, ip string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = ip + ":1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func doRequestWithXFF(handler http.Handler, remoteIP, xffIP string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteIP + ":1234"
	req.Header.Set("X-Forwarded-For", xffIP)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// TestWithRateLimit_Disabled verifies the handler passes through when rate limiting is disabled.
func TestWithRateLimit_Disabled(t *testing.T) {
	cfg := models.RateLimitConfiguration{Enabled: false}
	handler := WithRateLimit(cfg, okHandler())
	rr := doRequest(handler, "1.2.3.4")
	assert.Equal(t, http.StatusOK, rr.Code)
}

// TestWithRateLimit_ZeroRPS verifies the handler passes through when RequestsPerMinute is 0.
func TestWithRateLimit_ZeroRPS(t *testing.T) {
	cfg := models.RateLimitConfiguration{
		Enabled:           true,
		RequestsPerMinute: 0,
	}
	handler := WithRateLimit(cfg, okHandler())
	rr := doRequest(handler, "1.2.3.4")
	assert.Equal(t, http.StatusOK, rr.Code)
}

// TestWithRateLimit_AllowsUnderLimit verifies requests within the limit are allowed.
func TestWithRateLimit_AllowsUnderLimit(t *testing.T) {
	clearState()
	cfg := rateLimitConfig(600, 5, models.RateLimitActionThrottle)
	handler := WithRateLimit(cfg, okHandler())
	for range 5 {
		rr := doRequest(handler, "10.0.0.1")
		assert.Equal(t, http.StatusOK, rr.Code)
	}
}

// TestWithRateLimit_ThrottleOnExceed returns 429 when burst is exhausted and action is throttle.
func TestWithRateLimit_ThrottleOnExceed(t *testing.T) {
	clearState()
	// 60 RPM with burst 2 → allows 2 immediate requests, then 429
	cfg := rateLimitConfig(60, 2, models.RateLimitActionThrottle)
	handler := WithRateLimit(cfg, okHandler())
	doRequest(handler, "10.0.0.2")
	doRequest(handler, "10.0.0.2")

	rr := doRequest(handler, "10.0.0.2")
	assert.Equal(t, http.StatusTooManyRequests, rr.Code)
}

// TestWithRateLimit_BlockOnExceed blocks the IP and returns 403 after burst is exhausted.
func TestWithRateLimit_BlockOnExceed(t *testing.T) {
	clearState()
	cfg := rateLimitConfig(60, 1, models.RateLimitActionBlock)
	handler := WithRateLimit(cfg, okHandler())
	doRequest(handler, "10.0.0.3")

	// Second request exceeds burst → IP gets blocked → 403
	rr := doRequest(handler, "10.0.0.3")
	assert.Equal(t, http.StatusForbidden, rr.Code)

	// Subsequent requests from the same IP remain blocked
	rr = doRequest(handler, "10.0.0.3")
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

// TestWithRateLimit_BlockedIPRejected verifies a pre-blocked IP is rejected immediately.
func TestWithRateLimit_BlockedIPRejected(t *testing.T) {
	clearState()
	cfg := rateLimitConfig(600, 10, models.RateLimitActionBlock)
	blockIp("10.0.0.4", 5*time.Minute)

	handler := WithRateLimit(cfg, okHandler())
	rr := doRequest(handler, "10.0.0.4")
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

// TestWithRateLimit_BlockExpires verifies that an expired block no longer rejects requests.
func TestWithRateLimit_BlockExpires(t *testing.T) {
	clearState()
	cfg := rateLimitConfig(600, 10, models.RateLimitActionBlock)

	// Use a past time so the block is already expired
	blockIp("10.0.0.5", -1*time.Second)

	handler := WithRateLimit(cfg, okHandler())
	rr := doRequest(handler, "10.0.0.5")
	assert.Equal(t, http.StatusOK, rr.Code)
}

// TestWithRateLimit_WhitelistedIPBypass verifies whitelisted IPs skip rate limiting entirely.
func TestWithRateLimit_WhitelistedIPBypass(t *testing.T) {
	clearState()
	cfg := rateLimitConfig(1, 1, models.RateLimitActionThrottle)
	cfg.WhitelistIPs = []string{"192.168.1.1"}
	handler := WithRateLimit(cfg, okHandler())
	for range 10 {
		rr := doRequest(handler, "192.168.1.1")
		assert.Equal(t, http.StatusOK, rr.Code)
	}
}

// TestWithRateLimit_NoneActionAllows verifies action=none logs but still serves the request.
func TestWithRateLimit_NoneActionAllows(t *testing.T) {
	clearState()
	cfg := rateLimitConfig(60, 1, models.RateLimitActionNone)
	handler := WithRateLimit(cfg, okHandler())
	doRequest(handler, "10.0.0.6")

	// Second request exceeds burst, but action is none → still 200
	rr := doRequest(handler, "10.0.0.6")
	assert.Equal(t, http.StatusOK, rr.Code)
}

// TestWithRateLimit_IsolatedPerIP verifies rate limiters are per-IP and do not interfere.
func TestWithRateLimit_IsolatedPerIP(t *testing.T) {
	clearState()
	cfg := rateLimitConfig(60, 1, models.RateLimitActionThrottle)
	handler := WithRateLimit(cfg, okHandler())

	// Exhaust IP A's burst
	doRequest(handler, "10.1.0.1")
	rrA := doRequest(handler, "10.1.0.1")
	assert.Equal(t, http.StatusTooManyRequests, rrA.Code)

	// IP B should still have its own fresh burst
	rrB := doRequest(handler, "10.1.0.2")
	assert.Equal(t, http.StatusOK, rrB.Code)
}

// TestWithRateLimit_BurstExactBoundary sends exactly burst requests and verifies the
// (burst+1)th is the first to be rejected.
func TestWithRateLimit_BurstExactBoundary(t *testing.T) {
	const burst = 50
	clearState()
	cfg := rateLimitConfig(6000, burst, models.RateLimitActionThrottle)
	handler := WithRateLimit(cfg, okHandler())
	for i := range burst {
		rr := doRequest(handler, "20.0.0.1")
		assert.Equal(t, http.StatusOK, rr.Code, "request %d should be allowed", i+1)
	}

	rr := doRequest(handler, "20.0.0.1")
	assert.Equal(t, http.StatusTooManyRequests, rr.Code, "request burst+1 should be rejected")
}

// TestWithRateLimit_ManyIPsAllAllowed verifies 500 distinct IPs can each make requests
// without interfering with each other.
func TestWithRateLimit_ManyIPsAllAllowed(t *testing.T) {
	const ipCount = 500
	clearState()
	cfg := rateLimitConfig(60, 3, models.RateLimitActionThrottle)
	handler := WithRateLimit(cfg, okHandler())
	for i := range ipCount {
		ip := fmt.Sprintf("172.16.%d.%d", i/256, i%256)
		rr := doRequest(handler, ip)
		assert.Equal(t, http.StatusOK, rr.Code, "ip %s first request should be allowed", ip)
	}
}

// TestWithRateLimit_SustainedFloodThrottled sends 200 requests from one IP with a tight
// burst limit and verifies the majority are rejected under a throttle policy.
func TestWithRateLimit_SustainedFloodThrottled(t *testing.T) {
	const total = 200
	const burst = 5
	clearState()
	cfg := rateLimitConfig(60, burst, models.RateLimitActionThrottle)
	handler := WithRateLimit(cfg, okHandler())
	allowed, rejected := 0, 0
	for range total {
		rr := doRequest(handler, "30.0.0.1")
		if rr.Code == http.StatusOK {
			allowed++
		} else {
			rejected++
		}
	}

	assert.Equal(t, burst, allowed, "only burst-size requests should be allowed")
	assert.Equal(t, total-burst, rejected, "remaining requests should be rejected")
}

// TestWithRateLimit_SustainedFloodBlocked verifies that once an IP triggers the block
// action, all subsequent requests from that IP are rejected with 403.
func TestWithRateLimit_SustainedFloodBlocked(t *testing.T) {
	const total = 200
	const burst = 5
	clearState()
	cfg := rateLimitConfig(60, burst, models.RateLimitActionBlock)
	handler := WithRateLimit(cfg, okHandler())

	statuses := make([]int, total)
	for i := range total {
		statuses[i] = doRequest(handler, "30.0.0.2").Code
	}

	// First burst requests are allowed
	for i := range burst {
		assert.Equal(t, http.StatusOK, statuses[i], "request %d should be allowed", i+1)
	}
	// All requests after burst are forbidden (either by rate limit block or existing block)
	for i := burst; i < total; i++ {
		assert.Equal(t, http.StatusForbidden, statuses[i], "request %d should be forbidden", i+1)
	}
}

// TestWithRateLimit_MultipleIPsSomethingBlocked simulates a scenario where aggressive
// IPs get blocked while well-behaved IPs continue to be served.
func TestWithRateLimit_MultipleIPsSomethingBlocked(t *testing.T) {
	const burst = 3
	clearState()
	cfg := rateLimitConfig(60, burst, models.RateLimitActionBlock)
	handler := WithRateLimit(cfg, okHandler())

	aggressiveIP := "40.0.0.1"
	wellBehavedIP := "40.0.0.2"

	// Aggressive IP floods until blocked
	for range burst + 5 {
		doRequest(handler, aggressiveIP)
	}

	// Aggressive IP should now be blocked
	rr := doRequest(handler, aggressiveIP)
	assert.Equal(t, http.StatusForbidden, rr.Code)

	// Well-behaved IP is unaffected
	rr = doRequest(handler, wellBehavedIP)
	assert.Equal(t, http.StatusOK, rr.Code)
}

// TestWithRateLimit_ConcurrentRequestsSingleIP sends concurrent requests from a single IP
// and verifies the total allowed count never exceeds the burst size.
func TestWithRateLimit_ConcurrentRequestsSingleIP(t *testing.T) {
	const goroutines = 100
	const burst = 10
	clearState()
	cfg := rateLimitConfig(6000, burst, models.RateLimitActionThrottle)
	handler := WithRateLimit(cfg, okHandler())

	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			rr := doRequest(handler, "50.0.0.1")
			if rr.Code == http.StatusOK {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()

	assert.LessOrEqual(t, allowed.Load(), int64(burst),
		"concurrent allowed count must not exceed burst size")
}

// TestWithRateLimit_ConcurrentRequestsManyIPs sends concurrent requests from 100 distinct
// IPs and verifies all first requests succeed.
func TestWithRateLimit_ConcurrentRequestsManyIPs(t *testing.T) {
	const ipCount = 100
	clearState()
	cfg := rateLimitConfig(60, 1, models.RateLimitActionThrottle)
	handler := WithRateLimit(cfg, okHandler())

	var failed atomic.Int64
	var wg sync.WaitGroup
	for i := range ipCount {
		ip := fmt.Sprintf("60.0.%d.1", i)
		wg.Go(func() {
			rr := doRequest(handler, ip)
			if rr.Code != http.StatusOK {
				failed.Add(1)
			}
		})
	}
	wg.Wait()

	assert.Equal(t, int64(0), failed.Load(),
		"each IP's first request should succeed regardless of concurrency")
}

// TestWithRateLimit_RateLimitRecovery verifies that after waiting for the token bucket to
// refill, a previously exhausted IP can make requests again (throttle policy).
func TestWithRateLimit_RateLimitRecovery(t *testing.T) {
	// 120 RPM = 2 tokens/sec, burst 1
	clearState()
	cfg := rateLimitConfig(120, 1, models.RateLimitActionThrottle)
	handler := WithRateLimit(cfg, okHandler())

	// Exhaust the burst
	doRequest(handler, "70.0.0.1")
	rr := doRequest(handler, "70.0.0.1")
	assert.Equal(t, http.StatusTooManyRequests, rr.Code, "should be throttled after burst")

	// Wait for at least one token to be refilled (120 RPM → 0.5s per token)
	time.Sleep(600 * time.Millisecond)

	rr = doRequest(handler, "70.0.0.1")
	assert.Equal(t, http.StatusOK, rr.Code, "should recover after token refill")
}

// TestWithRateLimit_WhitelistAmongHighTraffic verifies whitelisted IPs are never rejected
// even while non-whitelisted IPs are being aggressively rate-limited.
func TestWithRateLimit_WhitelistAmongHighTraffic(t *testing.T) {
	const burst = 2
	clearState()
	cfg := rateLimitConfig(60, burst, models.RateLimitActionBlock)
	cfg.WhitelistIPs = []string{"80.0.0.1"}
	handler := WithRateLimit(cfg, okHandler())

	// Flood non-whitelisted IPs to trigger blocks
	for i := range 20 {
		ip := fmt.Sprintf("80.0.1.%d", i%5)
		doRequest(handler, ip)
	}

	// Whitelisted IP should always succeed regardless of traffic volume
	for range 50 {
		rr := doRequest(handler, "80.0.0.1")
		assert.Equal(t, http.StatusOK, rr.Code)
	}
}

// TestWithRateLimit_MultipleWhitelistedIPs verifies all entries in the whitelist bypass limiting.
func TestWithRateLimit_MultipleWhitelistedIPs(t *testing.T) {
	clearState()
	cfg := rateLimitConfig(1, 1, models.RateLimitActionThrottle)
	cfg.WhitelistIPs = []string{"192.168.2.1", "192.168.2.2"}
	handler := WithRateLimit(cfg, okHandler())

	for range 10 {
		rr1 := doRequest(handler, "192.168.2.1")
		assert.Equal(t, http.StatusOK, rr1.Code)
		rr2 := doRequest(handler, "192.168.2.2")
		assert.Equal(t, http.StatusOK, rr2.Code)
	}
}

// TestWithRateLimit_XForwardedForRateTracking verifies rate limiting keys on the
// X-Forwarded-For IP, so two requests from different proxies sharing the same client IP
// consume from the same token bucket.
func TestWithRateLimit_XForwardedForRateTracking(t *testing.T) {
	clearState()
	// burst 1: allows exactly one token
	cfg := rateLimitConfig(60, 1, models.RateLimitActionThrottle)
	handler := WithRateLimit(cfg, okHandler())

	// First request: client 1.2.3.4 via proxy A → allowed
	rr1 := doRequestWithXFF(handler, "10.10.0.1", "1.2.3.4")
	assert.Equal(t, http.StatusOK, rr1.Code)

	// Second request: same client IP via a different proxy → throttled (same bucket)
	rr2 := doRequestWithXFF(handler, "10.10.0.2", "1.2.3.4")
	assert.Equal(t, http.StatusTooManyRequests, rr2.Code,
		"requests from the same forwarded IP should share the same rate limit bucket")

	// A different client IP via the same proxy → still has its own full burst
	rr3 := doRequestWithXFF(handler, "10.10.0.1", "5.6.7.8")
	assert.Equal(t, http.StatusOK, rr3.Code,
		"a different forwarded IP should have its own independent bucket")
}

// TestWithRateLimit_UnrecognizedAction verifies that an unrecognized OnLimitExceeded value
// falls through to the default case and returns 429.
func TestWithRateLimit_UnrecognizedAction(t *testing.T) {
	clearState()
	cfg := rateLimitConfig(60, 1, models.RateLimitExceededAction("badaction"))
	handler := WithRateLimit(cfg, okHandler())

	doRequest(handler, "90.0.0.1")
	rr := doRequest(handler, "90.0.0.1")
	assert.Equal(t, http.StatusTooManyRequests, rr.Code)
}

// TestBlockIp_ExponentialBackoff verifies that successive blocks double the block duration.
func TestBlockIp_ExponentialBackoff(t *testing.T) {
	const base = time.Second
	ip := "100.0.0.1"

	for offense := 1; offense <= 5; offense++ {
		clearState()

		// Apply the block 'offense' times in sequence
		for range offense {
			blockIp(ip, base)
		}

		until, offenses := blockInfo(ip)

		expectedFactor := time.Duration(1 << (offense - 1))
		expectedMin := base * expectedFactor

		remaining := time.Until(until)
		assert.GreaterOrEqual(t, remaining, expectedMin-50*time.Millisecond,
			"offense %d: block duration should be at least %v, got ~%v", offense, expectedMin, remaining)
		assert.Equal(t, offense, offenses,
			"offense %d: offense counter should be %d", offense, offense)
	}
}

// TestWithRateLimit_BackoffIncreasesOnReblock verifies that when an IP is unblocked and
// then re-triggers the block action, the new block duration is longer than the first.
func TestWithRateLimit_BackoffIncreasesOnReblock(t *testing.T) {
	clearState()
	const base = 100 * time.Millisecond
	cfg := rateLimitConfig(60, 1, models.RateLimitActionBlock)
	cfg.BlockDuration = base
	handler := WithRateLimit(cfg, okHandler())

	// First offense: exhaust burst → blocked
	doRequest(handler, "101.0.0.1") // uses the 1 token
	doRequest(handler, "101.0.0.1") // rate exceeded → block #1

	firstUntil, firstOffenses := blockInfo("101.0.0.1")
	assert.Equal(t, 1, firstOffenses)

	// Simulate expiry by back-dating the block
	expireBlock("101.0.0.1")

	// The limiter still exists and has no tokens; the next Allow() call will fail
	// → second offense block is recorded
	doRequest(handler, "101.0.0.1")

	secondUntil, secondOffenses := blockInfo("101.0.0.1")
	assert.Equal(t, 2, secondOffenses)
	assert.True(t, secondUntil.After(firstUntil),
		"second block should expire later than the first (backoff applied)")
}

// TestWithRateLimit_BackoffCappedAtMax verifies that the block duration does not grow
// beyond maxBackoffFactor × baseDuration.
func TestWithRateLimit_BackoffCappedAtMax(t *testing.T) {
	const base = time.Millisecond
	ip := "102.0.0.1"

	// Apply far more blocks than maxBackoffFactor
	for range maxBackoffFactor + 10 {
		blockIp(ip, base)
	}

	until, _ := blockInfo(ip)

	maxDuration := base * time.Duration(1<<maxBackoffFactor)
	remaining := time.Until(until)
	// Allow a small margin for test execution time
	assert.LessOrEqual(t, remaining, maxDuration+50*time.Millisecond,
		"block duration must not exceed the cap of %v", maxDuration)
}
