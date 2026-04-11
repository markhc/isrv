package middleware

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/utils"
	"golang.org/x/time/rate"
)

const (
	maxBackoffFactor = 32
	visitorTTL       = 10 * time.Minute
	cleanupInterval  = 5 * time.Minute
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

	return rl
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

// RateLimit returns a middleware that enforces per-IP rate limiting based on config.
func RateLimit(ctx context.Context, config models.RateLimitConfiguration) func(http.Handler) http.Handler {
	rl := newRateLimiter(ctx, config)

	return func(next http.Handler) http.Handler {
		if !config.Enabled || config.RequestsPerMinute <= 0 {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ipAddress := utils.GetIPAddress(r, config.TrustedProxies)

			if slices.Contains(config.WhitelistIPs, ipAddress) {
				next.ServeHTTP(w, r)

				return
			}

			if rl.isBlocked(ipAddress) {
				logging.LogWarn("blocked request from IP", logging.String("ip_address", ipAddress))
				http.Error(w, "Rejected", http.StatusForbidden)

				return
			}

			limiter := rl.getLimiter(ipAddress)
			if !limiter.Allow() {
				logging.LogWarn("rate limit exceeded", logging.String("ip_address", ipAddress))

				switch config.OnLimitExceeded {
				case models.RateLimitActionBlock:
					rl.blockIP(ipAddress, config.BlockDuration)
					http.Error(w, "Rejected", http.StatusForbidden)

					return
				case models.RateLimitActionThrottle:
					http.Error(w, "Too Many Requests", http.StatusTooManyRequests)

					return
				case models.RateLimitActionNone:
					// log only, allow through
				default:
					http.Error(w, "Too Many Requests", http.StatusTooManyRequests)

					return
				}
			}

			next.ServeHTTP(w, r)
		})
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

func (rl *rateLimiter) blockIP(ip string, baseDuration time.Duration) {
	rl.blockMu.Lock()
	defer rl.blockMu.Unlock()

	entry := rl.blockList[ip]
	entry.offenses++

	factor := 1 << min(entry.offenses-1, maxBackoffFactor)
	entry.until = time.Now().Add(baseDuration * time.Duration(factor))

	rl.blockList[ip] = entry
}
