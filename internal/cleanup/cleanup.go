package cleanup

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/storage"
)

// Service periodically scans for expired files and removes them from both
// storage and the database.
type Service struct {
	db       database.Database
	storage  storage.Storage
	interval time.Duration
	enabled  bool

	wg sync.WaitGroup
}

// NewService creates a new cleanup Service with the given database, storage backend,
// enabled flag, and polling interval.
func NewService(db database.Database, storage storage.Storage, enabled bool, interval time.Duration) *Service {
	return &Service{
		db:       db,
		storage:  storage,
		enabled:  enabled,
		interval: interval,
	}
}

// Start launches the background cleanup goroutine. It is a no-op if the service
// is disabled.
func (s *Service) Start(ctx context.Context) context.CancelFunc {
	if !s.enabled {
		logging.LogInfo("file cleanup service is disabled")

		return nil
	}

	cancellableCtx, cancel := context.WithCancel(ctx)
	s.wg.Add(1)
	go s.cleanupLoop(cancellableCtx)

	logging.LogInfo("file cleanup service started", logging.String("interval", s.interval.String()))

	return cancel
}

// Join waits for the cleanup service to finish any ongoing cleanup cycles.
// It should be called after the context passed to Start is cancelled to ensure a graceful shutdown.
func (s *Service) Join() {
	if !s.enabled {
		return
	}

	s.wg.Wait()
}

func (s *Service) cleanupLoop(ctx context.Context) {
	defer s.wg.Done()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.performCleanup(ctx)
		}
	}
}

func (s *Service) performCleanup(ctx context.Context) {
	logging.LogDebug("starting cleanup cycle")

	expiredFiles, err := s.db.GetExpiredFiles()
	if err != nil {
		logging.LogError("failed to get expired files", logging.Error(err))

		return
	}

	if len(expiredFiles) == 0 {
		logging.LogDebug("no expired files found")

		return
	}

	logging.LogInfo("found expired files", logging.Int("count", len(expiredFiles)))

	successCount := 0
	failureCount := 0

	for _, fileID := range expiredFiles {
		if err := s.cleanupFile(ctx, fileID); err != nil {
			logging.LogError("failed to cleanup file",
				logging.String("file_id", fileID),
				logging.Error(err))
			failureCount++
		} else {
			successCount++
		}
	}

	logging.LogInfo("cleanup cycle completed",
		logging.Int("success", successCount),
		logging.Int("failures", failureCount))
}

func (s *Service) cleanupFile(ctx context.Context, fileID string) error {
	// Create a context with timeout for the storage operation
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := s.storage.DeleteFile(ctx, fileID)
	if err != nil {
		logging.LogError("failed to delete file from storage",
			logging.String("file_id", fileID),
			logging.Error(err))

		// Still try to delete from database even if storage deletion failed
	}

	// Delete from database
	dbErr := s.db.OnFileDelete(fileID)
	if dbErr != nil {
		logging.LogError("failed to delete file from database",
			logging.String("file_id", fileID),
			logging.Error(dbErr))

		// If storage deletion succeeded but database deletion failed,
		// we still consider it a partial failure
		if err == nil {
			return fmt.Errorf("failed to delete file from database: %w", dbErr)
		}
	}

	// If both operations failed, return the storage error as primary
	if err != nil {
		return fmt.Errorf("failed to delete file from storage: %w", err)
	}

	logging.LogDebug("successfully cleaned up file", logging.String("file_id", fileID))

	return nil
}
