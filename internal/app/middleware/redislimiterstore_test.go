package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func clusterCfg(addr string) models.ClusterConfiguration {
	return models.ClusterConfiguration{
		Enabled:      true,
		IPHashSecret: "test-hmac-secret",
		Redis: models.ClusterRedisConfiguration{
			Address: addr,
		},
	}
}

// newRedisStore builds a redisLimiterStore against a live miniredis.
func newRedisStore(t *testing.T, mr *miniredis.Miniredis, namespace string) *redisLimiterStore {
	t.Helper()
	s := newRedisLimiterStore(t.Context(), clusterCfg(mr.Addr()), namespace)
	t.Cleanup(func() { _ = s.client.Close() })
	return s
}

// newDeadRedisStore builds a store pointing at a port nothing listens on.
func newDeadRedisStore(t *testing.T, namespace string) *redisLimiterStore {
	t.Helper()
	s := newRedisLimiterStore(t.Context(), clusterCfg("127.0.0.1:1"), namespace)
	t.Cleanup(func() { _ = s.client.Close() })
	return s
}

// ---------------------------------------------------------------------------
// GCRA token bucket
// ---------------------------------------------------------------------------

func TestRedisStore_AllowsBurstThenDenies(t *testing.T) {
	mr := miniredis.RunT(t)
	s := newRedisStore(t, mr, limiterNamespaceHTTP)

	const burst = 3
	for i := range burst {
		allowed, err := s.Allow(t.Context(), "10.0.0.1", 1.0/3600, burst)
		require.NoError(t, err)
		assert.True(t, allowed, "request %d should be within burst", i+1)
	}

	allowed, err := s.Allow(t.Context(), "10.0.0.1", 1.0/3600, burst)
	require.NoError(t, err)
	assert.False(t, allowed, "request burst+1 should be denied")
}

func TestRedisStore_RecoversAfterWait(t *testing.T) {
	mr := miniredis.RunT(t)
	s := newRedisStore(t, mr, limiterNamespaceHTTP)

	// 10 tokens/sec, burst 1: one request allowed, then denied until ~100ms pass.
	allowed, err := s.Allow(t.Context(), "10.0.0.2", 10, 1)
	require.NoError(t, err)
	require.True(t, allowed)

	allowed, err = s.Allow(t.Context(), "10.0.0.2", 10, 1)
	require.NoError(t, err)
	require.False(t, allowed, "second immediate request should be denied")

	time.Sleep(150 * time.Millisecond)

	allowed, err = s.Allow(t.Context(), "10.0.0.2", 10, 1)
	require.NoError(t, err)
	assert.True(t, allowed, "should recover after the emission interval")
}

func TestRedisStore_BucketsIsolatedPerKey(t *testing.T) {
	mr := miniredis.RunT(t)
	s := newRedisStore(t, mr, limiterNamespaceHTTP)

	allowed, err := s.Allow(t.Context(), "10.0.0.3", 1.0/3600, 1)
	require.NoError(t, err)
	require.True(t, allowed)

	allowed, err = s.Allow(t.Context(), "10.0.0.3", 1.0/3600, 1)
	require.NoError(t, err)
	require.False(t, allowed, "key A should be exhausted")

	allowed, err = s.Allow(t.Context(), "10.0.0.4", 1.0/3600, 1)
	require.NoError(t, err)
	assert.True(t, allowed, "key B should have its own fresh bucket")
}

// ---------------------------------------------------------------------------
// Block list and offense escalation
// ---------------------------------------------------------------------------

func TestRedisStore_BlockAndExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	s := newRedisStore(t, mr, limiterNamespaceHTTP)

	blocked, err := s.IsBlocked(t.Context(), "10.1.0.1")
	require.NoError(t, err)
	require.False(t, blocked, "fresh key must not be blocked")

	require.NoError(t, s.Block(t.Context(), "10.1.0.1", time.Second))

	blocked, err = s.IsBlocked(t.Context(), "10.1.0.1")
	require.NoError(t, err)
	assert.True(t, blocked, "key should be blocked after Block")

	// The block key's TTL is the expiry: past it the key is unblocked.
	mr.FastForward(1100 * time.Millisecond)

	blocked, err = s.IsBlocked(t.Context(), "10.1.0.1")
	require.NoError(t, err)
	assert.False(t, blocked, "block should expire with its TTL")
}

func TestRedisStore_BlockNonPositiveBaseNeverBlocks(t *testing.T) {
	mr := miniredis.RunT(t)
	s := newRedisStore(t, mr, limiterNamespaceHTTP)

	require.NoError(t, s.Block(t.Context(), "10.1.0.9", 0))

	blocked, err := s.IsBlocked(t.Context(), "10.1.0.9")
	require.NoError(t, err)
	assert.False(t, blocked, "a zero base duration must never block")

	// SETting with a zero expiration would persist the block key forever, so
	// the store must not create one at all.
	for _, key := range mr.Keys() {
		assert.NotContains(t, key, "block:", "no block key should exist for a non-positive duration")
	}
}

func TestRedisStore_BlockEscalatesWithOffenses(t *testing.T) {
	mr := miniredis.RunT(t)
	s := newRedisStore(t, mr, limiterNamespaceHTTP)
	const base = time.Second

	// Offense 1: blocked for base; expired after base+ε.
	require.NoError(t, s.Block(t.Context(), "10.1.0.2", base))
	mr.FastForward(base + 100*time.Millisecond)

	blocked, err := s.IsBlocked(t.Context(), "10.1.0.2")
	require.NoError(t, err)
	require.False(t, blocked, "first block should have expired")

	// Offense 2 doubles the duration: still blocked after base+ε, gone after 2*base.
	require.NoError(t, s.Block(t.Context(), "10.1.0.2", base))
	mr.FastForward(base + 100*time.Millisecond)

	blocked, err = s.IsBlocked(t.Context(), "10.1.0.2")
	require.NoError(t, err)
	assert.True(t, blocked, "second offense should block for twice the base duration")

	mr.FastForward(base)

	blocked, err = s.IsBlocked(t.Context(), "10.1.0.2")
	require.NoError(t, err)
	assert.False(t, blocked, "escalated block should expire after 2x base")
}

func TestBlockDuration(t *testing.T) {
	tests := []struct {
		name     string
		base     time.Duration
		offenses int64
		want     time.Duration
	}{
		{"first offense", time.Minute, 1, time.Minute},
		{"second offense doubles", time.Minute, 2, 2 * time.Minute},
		{"fifth offense", time.Minute, 5, 16 * time.Minute},
		{"factor capped", time.Nanosecond, maxBackoffFactor + 10, time.Nanosecond << maxBackoffFactor},
		{"overflow clamped", 5 * time.Minute, maxBackoffFactor + 1, maxBlockDuration},
		{"non-positive base never blocks", 0, 3, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, blockDuration(tt.base, tt.offenses))
		})
	}
}

// ---------------------------------------------------------------------------
// Namespacing: general vs failed-login limiter state must never merge
// ---------------------------------------------------------------------------

func TestRedisStore_NamespacesAreIsolated(t *testing.T) {
	mr := miniredis.RunT(t)
	httpStore := newRedisStore(t, mr, limiterNamespaceHTTP)
	loginStore := newRedisStore(t, mr, limiterNamespaceLogin)
	const ip = "10.2.0.1"

	// A block in the http namespace is invisible to the login namespace.
	require.NoError(t, httpStore.Block(t.Context(), ip, time.Hour))

	blocked, err := httpStore.IsBlocked(t.Context(), ip)
	require.NoError(t, err)
	require.True(t, blocked)

	blocked, err = loginStore.IsBlocked(t.Context(), ip)
	require.NoError(t, err)
	assert.False(t, blocked, "login namespace must not see http-namespace blocks")

	// Exhausting the http bucket leaves the login bucket untouched.
	allowed, err := httpStore.Allow(t.Context(), ip, 1.0/3600, 1)
	require.NoError(t, err)
	require.True(t, allowed)

	allowed, err = httpStore.Allow(t.Context(), ip, 1.0/3600, 1)
	require.NoError(t, err)
	require.False(t, allowed, "http bucket should be exhausted")

	allowed, err = loginStore.Allow(t.Context(), ip, 1.0/3600, 1)
	require.NoError(t, err)
	assert.True(t, allowed, "login namespace must have its own bucket")
}

// ---------------------------------------------------------------------------
// Privacy: raw client IPs must never appear in Redis keys
// ---------------------------------------------------------------------------

func TestRedisStore_KeysNeverContainRawIP(t *testing.T) {
	mr := miniredis.RunT(t)
	s := newRedisStore(t, mr, limiterNamespaceHTTP)
	const ip = "203.0.113.7"

	_, err := s.Allow(t.Context(), ip, 1, 5)
	require.NoError(t, err)
	require.NoError(t, s.Block(t.Context(), ip, time.Minute))

	keys := mr.Keys()
	require.NotEmpty(t, keys, "operations should have created Redis keys")
	for _, key := range keys {
		assert.NotContains(t, key, ip, "raw IP leaked into Redis key %q", key)
		assert.Contains(t, key, "isrv:http:", "key %q should carry the configured prefix and namespace", key)
	}
}

func TestRedisStore_HashingIsDeterministicPerSecret(t *testing.T) {
	mr := miniredis.RunT(t)
	s := newRedisStore(t, mr, limiterNamespaceHTTP)

	// Same secret, same key → same bucket: the second call sees spent budget.
	allowed, err := s.Allow(t.Context(), "10.3.0.1", 1.0/3600, 1)
	require.NoError(t, err)
	require.True(t, allowed)

	other := newRedisStore(t, mr, limiterNamespaceHTTP)
	allowed, err = other.Allow(t.Context(), "10.3.0.1", 1.0/3600, 1)
	require.NoError(t, err)
	assert.False(t, allowed, "replicas sharing the secret must share buckets")
}

// ---------------------------------------------------------------------------
// Fail-open to the per-replica memory fallback
// ---------------------------------------------------------------------------

func TestRedisStore_DeadRedisFallsBackToMemoryLimiter(t *testing.T) {
	s := newDeadRedisStore(t, limiterNamespaceHTTP)
	const burst = 2

	// The fallback still enforces the limit: burst allowed, then denied.
	for i := range burst {
		allowed, err := s.Allow(t.Context(), "10.4.0.1", 1.0/3600, burst)
		require.NoError(t, err, "store must not surface Redis errors")
		assert.True(t, allowed, "request %d should be within the fallback burst", i+1)
	}

	allowed, err := s.Allow(t.Context(), "10.4.0.1", 1.0/3600, burst)
	require.NoError(t, err)
	assert.False(t, allowed, "fallback must still deny past the burst")
}

func TestRedisStore_DeadRedisBlocksViaFallback(t *testing.T) {
	s := newDeadRedisStore(t, limiterNamespaceHTTP)

	blocked, err := s.IsBlocked(t.Context(), "10.4.0.2")
	require.NoError(t, err)
	require.False(t, blocked)

	require.NoError(t, s.Block(t.Context(), "10.4.0.2", time.Hour))

	blocked, err = s.IsBlocked(t.Context(), "10.4.0.2")
	require.NoError(t, err)
	assert.True(t, blocked, "blocks must survive in the fallback while Redis is down")
	assert.Equal(t, int64(1), s.blockListSize(), "gauge should report the local fallback view")
}

// ---------------------------------------------------------------------------
// Middleware end-to-end with cluster mode enabled
// ---------------------------------------------------------------------------

// newClusterApp mirrors newApp but wires the RateLimit middleware to the given
// Redis address through the cluster configuration.
func newClusterApp(t *testing.T, c models.RateLimitConfiguration, redisAddr string) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{
		ProxyHeader: "X-Test-IP",
		TrustProxy:  true,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Proxies: []string{"0.0.0.0/0"},
		},
	})
	app.Use(RateLimit(t.Context(), c, clusterCfg(redisAddr)))
	app.Get("/", func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	return app
}

func TestRateLimit_ClusterMode_ThrottleAfterBurst(t *testing.T) {
	mr := miniredis.RunT(t)
	app := newClusterApp(t, cfg(60, 2, models.RateLimitActionThrottle), mr.Addr())

	assert.Equal(t, http.StatusOK, do(app, req("20.1.0.1")))
	assert.Equal(t, http.StatusOK, do(app, req("20.1.0.1")))
	assert.Equal(t, http.StatusTooManyRequests, do(app, req("20.1.0.1")))
	assert.Equal(t, http.StatusOK, do(app, req("20.1.0.2")), "other IPs keep their own budget")
}

func TestRateLimit_ClusterMode_BlockAfterBurst(t *testing.T) {
	mr := miniredis.RunT(t)
	app := newClusterApp(t, cfg(60, 1, models.RateLimitActionBlock), mr.Addr())

	do(app, req("20.1.0.3"))
	assert.Equal(t, http.StatusForbidden, do(app, req("20.1.0.3")))
	assert.Equal(t, http.StatusForbidden, do(app, req("20.1.0.3")), "IP should stay blocked")

	// The block state lives in Redis, keyed by HMAC — never the raw IP.
	for _, key := range mr.Keys() {
		assert.False(t, strings.Contains(key, "20.1.0.3"), "raw IP leaked into Redis key %q", key)
	}
}

func TestRateLimit_ClusterMode_SharedStateAcrossInstances(t *testing.T) {
	// Two middleware instances against the same Redis stand in for two
	// replicas: budget spent through one is visible to the other.
	mr := miniredis.RunT(t)
	c := cfg(60, 2, models.RateLimitActionThrottle)
	replicaA := newClusterApp(t, c, mr.Addr())
	replicaB := newClusterApp(t, c, mr.Addr())

	assert.Equal(t, http.StatusOK, do(replicaA, req("20.2.0.1")))
	assert.Equal(t, http.StatusOK, do(replicaB, req("20.2.0.1")))
	assert.Equal(t, http.StatusTooManyRequests, do(replicaA, req("20.2.0.1")),
		"the burst budget must be shared across replicas")
}

func TestRateLimit_ClusterMode_DeadRedisServesAndStillLimits(t *testing.T) {
	// Nothing listens on this port: every store call degrades to the memory
	// fallback. Requests must neither 500 nor bypass limiting. 1 rpm keeps the
	// refill negligible even though each dead-Redis dial burns wall time.
	app := newClusterApp(t, cfg(1, 2, models.RateLimitActionThrottle), "127.0.0.1:1")

	statuses := make([]int, 0, 3)
	for range 3 {
		statuses = append(statuses, do(app, req("20.3.0.1")))
	}
	assert.Equal(t, []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests}, statuses,
		"dead Redis must degrade to per-replica limiting, not errors or unlimited traffic")
}

func TestRateLimitFailedLogins_ClusterMode_IndependentFromGeneralLimiter(t *testing.T) {
	// A block earned via failed logins must not leak into the general
	// limiter's namespace even though both share one Redis.
	mr := miniredis.RunT(t)

	adminCfg := models.AdminConfiguration{
		FailedLoginLimit:         2,
		FailedLoginBlockDuration: 12 * time.Hour,
	}
	app := fiber.New(fiber.Config{
		ProxyHeader: "X-Test-IP",
		TrustProxy:  true,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Proxies: []string{"0.0.0.0/0"},
		},
	})
	app.Use(RateLimitFailedLogins(t.Context(), adminCfg, clusterCfg(mr.Addr())))
	app.Post("/login", func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusUnauthorized)
	})

	generalApp := newClusterApp(t, cfg(600, 5, models.RateLimitActionThrottle), mr.Addr())

	const ip = "20.4.0.1"
	for range adminCfg.FailedLoginLimit + 1 {
		do(app, loginReq(ip, true))
	}
	assert.Equal(t, http.StatusForbidden, do(app, loginReq(ip, true)), "IP should be login-blocked")
	assert.Equal(t, http.StatusOK, do(generalApp, req(ip)),
		"login-blocked IP must still pass the general limiter")
}

// Guards against accidentally weakening the fractional-rate conversion: the
// failed-login limiter runs at a few events per hour and must not round to
// zero or to a whole event per second.
func TestToRedisRateLimit_FractionalRates(t *testing.T) {
	l := toRedisRateLimit(5.0/3600, 5)
	assert.Equal(t, 1, l.Rate)
	assert.Equal(t, 5, l.Burst)
	assert.Equal(t, 12*time.Minute, l.Period, "5/hour is one event per 12 minutes")

	l = toRedisRateLimit(2, 10)
	assert.Equal(t, fmt.Sprint(500*time.Millisecond), fmt.Sprint(l.Period), "2/sec is one event per 500ms")
}
