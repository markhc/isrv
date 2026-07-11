package middleware

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	maxBackoffFactor = 32
	visitorTTL       = 10 * time.Minute
	cleanupInterval  = 5 * time.Minute
)

// LimiterStore holds the mutable rate-limit state.
type LimiterStore interface {
	// Allow spends one token from key's bucket (ratePerSec refill, burst cap)
	// and reports whether the request is within the limit.
	Allow(ctx context.Context, key string, ratePerSec float64, burst int) (bool, error)
	// Block records an offense against key and blocks it.
	Block(ctx context.Context, key string, baseDuration time.Duration) error
	// IsBlocked reports whether key is currently blocked.
	IsBlocked(ctx context.Context, key string) (bool, error)
}

type visitorEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type blockEntry struct {
	until    time.Time
	offenses int
}

// memoryLimiterStore is the in-process LimiterStore: per-key token buckets and
// a block list held in maps, pruned by a TTL cleanup goroutine. It never
// returns errors.
type memoryLimiterStore struct {
	visitors   map[string]*visitorEntry
	visitorsMu sync.Mutex
	blockList  map[string]blockEntry
	blockMu    sync.Mutex
}

var _ LimiterStore = (*memoryLimiterStore)(nil)

func newMemoryLimiterStore(ctx context.Context) *memoryLimiterStore {
	s := &memoryLimiterStore{
		visitors:  make(map[string]*visitorEntry),
		blockList: make(map[string]blockEntry),
	}
	go s.cleanupLoop(ctx)

	return s
}

func (s *memoryLimiterStore) Allow(_ context.Context, key string, ratePerSec float64, burst int) (bool, error) {
	s.visitorsMu.Lock()

	entry, exists := s.visitors[key]
	if !exists {
		entry = &visitorEntry{
			limiter: rate.NewLimiter(rate.Limit(ratePerSec), burst),
		}
		s.visitors[key] = entry
	}

	entry.lastSeen = time.Now()
	s.visitorsMu.Unlock()

	// rate.Limiter is safe for concurrent use; don't hold the map lock here.
	return entry.limiter.Allow(), nil
}

func (s *memoryLimiterStore) Block(_ context.Context, key string, baseDuration time.Duration) error {
	s.blockMu.Lock()
	defer s.blockMu.Unlock()

	entry := s.blockList[key]
	entry.offenses++

	factor := 1 << min(entry.offenses-1, maxBackoffFactor)
	entry.until = time.Now().Add(baseDuration * time.Duration(factor))

	s.blockList[key] = entry

	return nil
}

func (s *memoryLimiterStore) IsBlocked(_ context.Context, key string) (bool, error) {
	s.blockMu.Lock()
	defer s.blockMu.Unlock()

	entry, exists := s.blockList[key]
	if !exists {
		return false, nil
	}

	return time.Now().Before(entry.until), nil
}

func (s *memoryLimiterStore) blockListSize() int64 {
	s.blockMu.Lock()
	defer s.blockMu.Unlock()

	return int64(len(s.blockList))
}

func (s *memoryLimiterStore) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanup()
		}
	}
}

func (s *memoryLimiterStore) cleanup() {
	now := time.Now()

	s.visitorsMu.Lock()
	for key, entry := range s.visitors {
		if now.Sub(entry.lastSeen) > visitorTTL {
			delete(s.visitors, key)
		}
	}
	s.visitorsMu.Unlock()

	s.blockMu.Lock()
	for key, entry := range s.blockList {
		if now.After(entry.until) {
			delete(s.blockList, key)
		}
	}
	s.blockMu.Unlock()
}
