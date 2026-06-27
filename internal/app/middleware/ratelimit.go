package middleware

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"golang.org/x/time/rate"
)

const (
	maxBackoffFactor = 32
	visitorTTL       = 10 * time.Minute
	cleanupInterval  = 5 * time.Minute

	// Rate-limit decision attribute values.
	decisionAllow    = "allow"
	decisionThrottle = "throttle"
	decisionBlock    = "block"
	decisionBlocked  = "blocked"
)

// Pre-allocated decision attribute sets to avoid per-request allocation in the hot path.
var (
	decisionAllowAttrs    = metric.WithAttributes(attribute.String(telemetry.AttrDecision, decisionAllow))
	decisionThrottleAttrs = metric.WithAttributes(attribute.String(telemetry.AttrDecision, decisionThrottle))
	decisionBlockAttrs    = metric.WithAttributes(attribute.String(telemetry.AttrDecision, decisionBlock))
	decisionBlockedAttrs  = metric.WithAttributes(attribute.String(telemetry.AttrDecision, decisionBlocked))
)

type visitorEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type blockEntry struct {
	until    time.Time
	offenses int
}

type rateLimiter struct {
	config     models.RateLimitConfiguration
	visitors   map[string]*visitorEntry
	visitorsMu sync.Mutex
	blockList  map[string]blockEntry
	blockMu    sync.Mutex
}

func newRateLimiter(ctx context.Context, config models.RateLimitConfiguration) *rateLimiter {
	rl := &rateLimiter{
		config:    config,
		visitors:  make(map[string]*visitorEntry),
		blockList: make(map[string]blockEntry),
	}
	go rl.cleanupLoop(ctx)

	if err := telemetry.RegisterBlocklistGauge(rl.blockListSize); err != nil {
		logging.ErrorCtx(ctx, "failed to register rate-limit blocklist gauge", logging.Error(err))
	}

	return rl
}

// RateLimit returns a Fiber middleware that enforces per-IP rate limiting based on config.
func RateLimit(ctx context.Context, config models.RateLimitConfiguration) fiber.Handler {
	rl := newRateLimiter(ctx, config)

	return func(c fiber.Ctx) error {
		if !config.Enabled || config.RequestsPerMinute <= 0 {
			return c.Next()
		}

		ipAddress := c.IP()

		if slices.Contains(config.WhitelistIPs, ipAddress) {
			telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionAllowAttrs)
			return c.Next()
		}

		if rl.isBlocked(ipAddress) {
			telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionBlockedAttrs)
			logging.WarnCtx(c.Context(), "blocked request from IP", logging.MaybeIP("ip_address", ipAddress))
			return c.Status(fiber.StatusForbidden).SendString("Rejected")
		}

		limiter := rl.getLimiter(ipAddress)
		if !limiter.Allow() {
			logging.WarnCtx(c.Context(), "rate limit exceeded", logging.MaybeIP("ip_address", ipAddress))

			switch config.OnLimitExceeded {
			case models.RateLimitActionBlock:
				telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionBlockAttrs)
				rl.blockIP(ipAddress, config.BlockDuration)
				return c.Status(fiber.StatusForbidden).SendString("Rejected")
			case models.RateLimitActionThrottle:
				telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionThrottleAttrs)
				return c.Status(fiber.StatusTooManyRequests).SendString("Too Many Requests")
			case models.RateLimitActionNone:
				telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionAllowAttrs)
				// Logged but otherwise allowed through.
			default:
				telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionThrottleAttrs)
				return c.Status(fiber.StatusTooManyRequests).SendString("Too Many Requests")
			}
		} else {
			telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionAllowAttrs)
		}

		return c.Next()
	}
}

func (rl *rateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.visitorsMu.Lock()
	defer rl.visitorsMu.Unlock()

	entry, exists := rl.visitors[ip]
	if !exists {
		entry = &visitorEntry{
			limiter: rate.NewLimiter(
				rate.Limit(rl.config.RequestsPerMinute)/60.0,
				rl.config.BurstSize,
			),
		}
		rl.visitors[ip] = entry
	}

	entry.lastSeen = time.Now()

	return entry.limiter
}

func (rl *rateLimiter) isBlocked(ip string) bool {
	rl.blockMu.Lock()
	defer rl.blockMu.Unlock()

	entry, exists := rl.blockList[ip]
	if !exists {
		return false
	}

	return time.Now().Before(entry.until)
}

// blockListSize reports the current number of IPs in the blocklist. It is
// exposed as a callback for the OTel observable gauge and is safe for
// concurrent use.
func (rl *rateLimiter) blockListSize() int64 {
	rl.blockMu.Lock()
	defer rl.blockMu.Unlock()

	return int64(len(rl.blockList))
}

func (rl *rateLimiter) blockIP(ip string, baseDuration time.Duration) {
	rl.blockMu.Lock()
	defer rl.blockMu.Unlock()

	entry := rl.blockList[ip]
	entry.offenses++

	factor := 1 << min(entry.offenses-1, maxBackoffFactor)
	entry.until = time.Now().Add(baseDuration * time.Duration(factor))

	rl.blockList[ip] = entry
}

func (rl *rateLimiter) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rl.cleanup()
		}
	}
}

func (rl *rateLimiter) cleanup() {
	now := time.Now()

	rl.visitorsMu.Lock()
	for ip, entry := range rl.visitors {
		if now.Sub(entry.lastSeen) > visitorTTL {
			delete(rl.visitors, ip)
		}
	}
	rl.visitorsMu.Unlock()

	rl.blockMu.Lock()
	for ip, entry := range rl.blockList {
		if now.After(entry.until) {
			delete(rl.blockList, ip)
		}
	}
	rl.blockMu.Unlock()
}
