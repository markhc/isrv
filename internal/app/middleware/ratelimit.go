package middleware

import (
	"context"
	"slices"
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

type rateLimiter struct {
	config     models.RateLimitConfiguration
	refillRate rate.Limit
	burst      int
	store      LimiterStore
}

func newRateLimiter(ctx context.Context, config models.RateLimitConfiguration, store LimiterStore) *rateLimiter {
	rl := &rateLimiter{
		config:     config,
		refillRate: rate.Limit(config.RequestsPerMinute) / 60.0,
		burst:      config.BurstSize,
		store:      store,
	}

	// Both store implementations expose a local blocklist size (the Redis
	// store reports its fallback's view; see redisLimiterStore.blockListSize).
	if sizer, ok := store.(interface{ blockListSize() int64 }); ok {
		if err := telemetry.RegisterBlocklistGauge(sizer.blockListSize); err != nil {
			logging.ErrorCtx(ctx, "failed to register rate-limit blocklist gauge", logging.Error(err))
		}
	}

	return rl
}

// RateLimit returns a Fiber middleware that enforces per-IP rate limiting based
// on config. With cluster mode enabled the limit state is shared across
// replicas through Redis; otherwise it is per-process.
func RateLimit(
	ctx context.Context, config models.RateLimitConfiguration, cluster models.ClusterConfiguration,
) fiber.Handler {
	rl := newRateLimiter(ctx, config, newLimiterStore(ctx, cluster, limiterNamespaceHTTP))

	return func(c fiber.Ctx) error {
		if !config.Enabled || config.RequestsPerMinute <= 0 {
			return c.Next()
		}

		ipAddress := c.IP()

		if slices.Contains(config.WhitelistIPs, ipAddress) {
			telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionAllowAttrs)
			return c.Next()
		}

		if rl.isBlocked(c.Context(), ipAddress) {
			telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionBlockedAttrs)
			logging.WarnCtx(c.Context(), "blocked request from IP", logging.MaybeIP("ip_address", ipAddress))
			return c.Status(fiber.StatusForbidden).SendString("Rejected")
		}

		if !rl.allow(c.Context(), ipAddress) {
			if handled, err := rl.applyLimitAction(c, ipAddress); handled {
				return err
			}
		} else {
			telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionAllowAttrs)
		}

		return c.Next()
	}
}

// RateLimitFailedLogins returns a Fiber middleware for authentication endpoints.
// Unlike RateLimit, it only spends rate-limit budget on requests the downstream
// handler rejects with 401 Unauthorized.
func RateLimitFailedLogins(
	ctx context.Context, config models.AdminConfiguration, cluster models.ClusterConfiguration,
) fiber.Handler {
	rl := newRateLimiter(ctx, models.RateLimitConfiguration{
		Enabled:         true,
		OnLimitExceeded: models.RateLimitActionBlock,
		BlockDuration:   config.FailedLoginBlockDuration,
	}, newLimiterStore(ctx, cluster, limiterNamespaceLogin))

	// Allow up to maxFailedLoginsPerHour failed attempts before blocking.
	rl.refillRate = rate.Every(time.Hour / time.Duration(config.FailedLoginLimit))
	rl.burst = config.FailedLoginLimit

	return func(c fiber.Ctx) error {
		ipAddress := c.IP()

		if rl.isBlocked(c.Context(), ipAddress) {
			telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionBlockedAttrs)
			logging.WarnCtx(c.Context(), "blocked request from IP", logging.MaybeIP("ip_address", ipAddress))
			return c.Status(fiber.StatusForbidden).SendString("Rejected")
		}

		// Run the handler first; only failed attempts count against the limit.
		if err := c.Next(); err != nil {
			return err
		}

		// Successful (or otherwise non-credential) attempts don't cost budget.
		if c.Response().StatusCode() != fiber.StatusUnauthorized {
			telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionAllowAttrs)
			return nil
		}

		if !rl.allow(c.Context(), ipAddress) {
			if handled, err := rl.applyLimitAction(c, ipAddress); handled {
				return err
			}
		}

		return nil
	}
}

// applyLimitAction enforces the configured OnLimitExceeded action for an IP that
// has exhausted its budget.
func (rl *rateLimiter) applyLimitAction(c fiber.Ctx, ipAddress string) (bool, error) {
	logging.WarnCtx(c.Context(), "rate limit exceeded", logging.MaybeIP("ip_address", ipAddress))

	switch rl.config.OnLimitExceeded {
	case models.RateLimitActionBlock:
		telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionBlockAttrs)
		rl.block(c.Context(), ipAddress)
		return true, c.Status(fiber.StatusForbidden).SendString("Rejected")
	case models.RateLimitActionThrottle:
		telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionThrottleAttrs)
		return true, c.Status(fiber.StatusTooManyRequests).SendString("Too Many Requests")
	case models.RateLimitActionNone:
		telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionAllowAttrs)
		return false, nil
	default:
		telemetry.RateLimitDecisions.Add(c.Context(), 1, decisionThrottleAttrs)
		return true, c.Status(fiber.StatusTooManyRequests).SendString("Too Many Requests")
	}
}

// Store errors fail open: a request is served rather than rejected when the
// store cannot answer. The in-memory store never errors; remote stores are
// expected to degrade to a local fallback themselves, so an error here is
// already exceptional and dropping rate limiting beats dropping traffic.

// allow spends one token from ipAddress's bucket, failing open on store errors.
func (rl *rateLimiter) allow(ctx context.Context, ipAddress string) bool {
	allowed, err := rl.store.Allow(ctx, ipAddress, float64(rl.refillRate), rl.burst)
	if err != nil {
		logging.WarnCtx(ctx, "rate-limit store Allow failed; failing open", logging.Error(err))
		return true
	}

	return allowed
}

// isBlocked reports whether ipAddress is blocked, failing open on store errors.
func (rl *rateLimiter) isBlocked(ctx context.Context, ipAddress string) bool {
	blocked, err := rl.store.IsBlocked(ctx, ipAddress)
	if err != nil {
		logging.WarnCtx(ctx, "rate-limit store IsBlocked failed; failing open", logging.Error(err))
		return false
	}

	return blocked
}

// block records an offense for ipAddress. A store error only means the block
// was not persisted; the offending request is still rejected by the caller.
func (rl *rateLimiter) block(ctx context.Context, ipAddress string) {
	if err := rl.store.Block(ctx, ipAddress, rl.config.BlockDuration); err != nil {
		logging.WarnCtx(ctx, "rate-limit store Block failed", logging.Error(err))
	}
}
