package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sync/atomic"
	"time"

	"github.com/go-redis/redis_rate/v10"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/telemetry"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Store namespaces for the two limiter instances. RateLimit and
// RateLimitFailedLogins must never share buckets, blocks, or offense counts
// (see LimiterStore), so each middleware bakes its namespace into the store
// at construction.
const (
	limiterNamespaceHTTP  = "http"
	limiterNamespaceLogin = "login"
)

const (
	// redisErrorLogInterval throttles the "degraded to memory limiter" log so
	// a Redis outage does not emit one warning per request.
	redisErrorLogInterval = 30 * time.Second
	// maxBlockDuration caps a block whose exponential backoff would overflow
	// time.Duration; at this point the block is effectively permanent anyway.
	maxBlockDuration = 10 * 365 * 24 * time.Hour
)

// Pre-allocated attribute sets for the Redis error counter, one per store
// operation, to avoid per-error allocation.
var (
	redisErrAllowAttrs = metric.WithAttributes(
		attribute.String(telemetry.AttrOperation, "allow"))
	redisErrBlockAttrs = metric.WithAttributes(
		attribute.String(telemetry.AttrOperation, "block"))
	redisErrIsBlockedAttrs = metric.WithAttributes(
		attribute.String(telemetry.AttrOperation, "is_blocked"))
)

// redisLimiterStore is the shared LimiterStore for multi-replica deployments:
// token buckets via redis_rate, blocks as SET PX keys (the TTL  is the block expiry),
// and offense counts as INCR+EXPIRE keys driving the exponential backoff.
//
// Client IPs never appear in Redis: every key component derived from the IP
// is HMAC-SHA256(ipHashSecret, ip). Equality is all the limiter needs, and
// hashing keeps raw IPs out of Redis memory, RDB/AOF files, and KEYS output.
//
// When Redis is unreachable the store degrades to a per-replica in-memory
// limiter instead of returning errors: rate limiting keeps working with
// single-replica semantics rather than 500ing requests or waving traffic
// through unlimited.
type redisLimiterStore struct {
	client  *redis.Client
	limiter *redis_rate.Limiter
	// prefix is "isrv:{namespace}:", e.g. "isrv:http:".
	prefix string
	secret []byte
	// fallback serves requests while Redis is unreachable.
	fallback *memoryLimiterStore
	// lastErrLogNano is the unix-nano timestamp of the last degraded-mode log.
	lastErrLogNano atomic.Int64
}

var _ LimiterStore = (*redisLimiterStore)(nil)

// newLimiterStore returns the LimiterStore matching the cluster configuration:
// the in-process memory store when cluster mode is disabled (single-replica,
// zero external dependencies), otherwise the Redis-backed store namespaced
// under namespace.
//
//nolint:ireturn
func newLimiterStore(ctx context.Context, cluster models.ClusterConfiguration, namespace string) LimiterStore {
	if !cluster.Enabled {
		return newMemoryLimiterStore(ctx)
	}

	return newRedisLimiterStore(ctx, cluster, namespace)
}

func newRedisLimiterStore(
	ctx context.Context, cluster models.ClusterConfiguration, namespace string,
) *redisLimiterStore {
	client := redis.NewClient(&redis.Options{
		Addr:     cluster.Redis.Address,
		Password: cluster.Redis.Password,
		DB:       cluster.Redis.DB,
		// Tight timeouts and a single retry bound the hot-path stall when
		// Redis is down; the memory fallback takes over from there.
		DialTimeout:  2 * time.Second,
		ReadTimeout:  time.Second,
		WriteTimeout: time.Second,
		MaxRetries:   1,
	})

	return &redisLimiterStore{
		client:   client,
		limiter:  redis_rate.NewLimiter(client),
		prefix:   "isrv:" + namespace + ":",
		secret:   []byte(cluster.IPHashSecret),
		fallback: newMemoryLimiterStore(ctx),
	}
}

func (s *redisLimiterStore) Allow(ctx context.Context, key string, ratePerSec float64, burst int) (bool, error) {
	res, err := s.limiter.Allow(ctx, s.prefix+s.hashKey(key), toRedisRateLimit(ratePerSec, burst))
	if err != nil {
		s.degrade(ctx, err, redisErrAllowAttrs)
		return s.fallback.Allow(ctx, key, ratePerSec, burst)
	}

	return res.Allowed > 0, nil
}

func (s *redisLimiterStore) Block(ctx context.Context, key string, baseDuration time.Duration) error {
	hashed := s.hashKey(key)
	offenseKey := s.prefix + "offense:" + hashed
	blockKey := s.prefix + "block:" + hashed

	offenses, err := s.client.Incr(ctx, offenseKey).Result()
	if err != nil {
		s.degrade(ctx, err, redisErrBlockAttrs)
		return s.fallback.Block(ctx, key, baseDuration)
	}

	duration := blockDuration(baseDuration, offenses)
	if duration <= 0 {
		return nil
	}

	pipe := s.client.Pipeline()
	pipe.Set(ctx, blockKey, "1", duration)
	pipe.Expire(ctx, offenseKey, duration+cleanupInterval)

	if _, err := pipe.Exec(ctx); err != nil {
		s.degrade(ctx, err, redisErrBlockAttrs)
		return s.fallback.Block(ctx, key, baseDuration)
	}

	return nil
}

func (s *redisLimiterStore) IsBlocked(ctx context.Context, key string) (bool, error) {
	n, err := s.client.Exists(ctx, s.prefix+"block:"+s.hashKey(key)).Result()
	if err != nil {
		s.degrade(ctx, err, redisErrIsBlockedAttrs)

		// fallback to in-memory store if Redis is unavailable, so that requests are not rejected
		return s.fallback.IsBlocked(ctx, key)
	}

	return n > 0, nil
}

func (s *redisLimiterStore) blockListSize() int64 {
	return s.fallback.blockListSize()
}

func (s *redisLimiterStore) hashKey(key string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(key))

	return hex.EncodeToString(mac.Sum(nil))
}

// degrade records a Redis failure: it increments the error counter on every
// occurrence but logs at most once per redisErrorLogInterval, so an outage
// shows up in metrics without spamming one warning per request.
func (s *redisLimiterStore) degrade(ctx context.Context, err error, attrs metric.MeasurementOption) {
	telemetry.ClusterRedisErrors.Add(ctx, 1, attrs)

	now := time.Now().UnixNano()

	last := s.lastErrLogNano.Load()
	if now-last < redisErrorLogInterval.Nanoseconds() || !s.lastErrLogNano.CompareAndSwap(last, now) {
		return
	}

	logging.WarnCtx(ctx, "redis rate-limit store unavailable; degraded to per-replica memory limiter",
		logging.Error(err))
}

// toRedisRateLimit converts the token-bucket parameters to redis_rate's GCRA limit.
func toRedisRateLimit(ratePerSec float64, burst int) redis_rate.Limit {
	return redis_rate.Limit{
		Rate:   1,
		Burst:  burst,
		Period: time.Duration(float64(time.Second) / ratePerSec),
	}
}

func blockDuration(base time.Duration, offenses int64) time.Duration {
	if base <= 0 {
		return 0
	}

	factor := int64(1) << min(offenses-1, maxBackoffFactor)
	if int64(base) > math.MaxInt64/factor {
		return maxBlockDuration
	}

	return time.Duration(int64(base) * factor)
}
