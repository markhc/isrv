package middleware

import (
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/utils"
	"golang.org/x/time/rate"
)

var (
	// map of IP addresses to their rate limiters.
	visitors   = make(map[string]*rate.Limiter)
	visitorsMu sync.Mutex
	// blockList keeps track of IPs that are currently blocked and when their block expires.
	blockList = make(map[string]time.Time)
	blockMu   sync.Mutex
)

func WithRateLimit(rateLimit models.RateLimitConfiguration, next http.Handler) http.Handler {
	if !rateLimit.Enabled || rateLimit.RequestsPerMinute <= 0 {
		// No rate limit configured, just pass through
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ipAddress := utils.GetIPAddress(r)

		// Check if the IP is whitelisted
		if slices.Contains(rateLimit.WhitelistIPs, ipAddress) {
			next.ServeHTTP(w, r)

			return
		}

		if isBlocked(ipAddress) {
			logging.LogWarn(
				"blocked request from IP",
				logging.String("ip_address", ipAddress),
			)
			http.Error(w, "Rejected", http.StatusForbidden)

			return
		}

		limiter := getLimiter(rateLimit, ipAddress)
		if !limiter.Allow() {
			logging.LogWarn(
				"rate limit exceeded",
				logging.String("ip_address", ipAddress),
			)

			switch rateLimit.OnLimitExceeded {
			case models.RateLimitActionBlock:
				// block the IP for the configured duration and error out
				blockIp(ipAddress, rateLimit.BlockDuration)
				http.Error(w, "Rejected", http.StatusForbidden)

				return
			case models.RateLimitActionThrottle:
				// For throttle, we could add a Retry-After header or similar, but for simplicity we'll just return 429
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)

				return
			case models.RateLimitActionNone:
				// Just log the event, but allow the request
			default:
				// Default to block if action is unrecognized
				http.Error(w, "Too Many Requests", http.StatusTooManyRequests)

				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func getLimiter(config models.RateLimitConfiguration, ip string) *rate.Limiter {
	visitorsMu.Lock()
	defer visitorsMu.Unlock()

	limiter, exists := visitors[ip]
	if !exists {
		limiter = rate.NewLimiter(
			rate.Limit(config.RequestsPerMinute)/60.0,
			config.BurstSize,
		)
		visitors[ip] = limiter
	}

	return limiter
}

func isBlocked(ip string) bool {
	blockMu.Lock()
	defer blockMu.Unlock()

	unblockTime, blocked := blockList[ip]
	if !blocked {
		return false
	}

	if time.Now().After(unblockTime) {
		delete(blockList, ip)

		return false
	}

	return true
}

func blockIp(ip string, duration time.Duration) {
	blockMu.Lock()
	defer blockMu.Unlock()

	blockList[ip] = time.Now().Add(duration)
}
