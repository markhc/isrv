package cleanup

import (
	"context"
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

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
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
func (s *Service) Start() {
	if !s.enabled {
		logging.LogInfo("File cleanup service is disabled")
		return
	}

	s.ctx, s.cancel = context.WithCancel(context.Background())

	s.wg.Add(1)
	go s.cleanupLoop()

	logging.LogInfo("File cleanup service started", logging.String("interval", s.interval.String()))
}

// Stop signals the cleanup goroutine to exit and waits for it to finish.
// It is a no-op if the service is disabled or was never started.
func (s *Service) Stop() {
	if !s.enabled || s.cancel == nil {
		return
	}

	logging.LogInfo("Stopping file cleanup service")
	s.cancel()
	s.wg.Wait()
	logging.LogInfo("File cleanup service stopped")
}

func (s *Service) cleanupLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.performCleanup()
		}
	}
}

func (s *Service) performCleanup() {
	logging.LogDebug("Starting cleanup cycle")

	expiredFiles, err := s.db.GetExpiredFiles()
	if err != nil {
		logging.LogError("Failed to get expired files", logging.Error(err))
		return
	}

	if len(expiredFiles) == 0 {
		logging.LogDebug("No expired files found")
		return
	}

	logging.LogInfo("Found expired files", logging.Int("count", len(expiredFiles)))

	successCount := 0
	failureCount := 0

	for _, fileID := range expiredFiles {
		if err := s.cleanupFile(fileID); err != nil {
			logging.LogError("Failed to cleanup file",
				logging.String("file_id", fileID),
				logging.Error(err))
			failureCount++
		} else {
			successCount++
		}
	}

	logging.LogInfo("Cleanup cycle completed",
		logging.Int("success", successCount),
		logging.Int("failures", failureCount))
}

func (s *Service) cleanupFile(fileID string) error {
	// Create a context with timeout for the storage operation
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := s.storage.DeleteFile(ctx, fileID)
	if err != nil {
		logging.LogError("Failed to delete file from storage",
			logging.String("file_id", fileID),
			logging.Error(err))

		// Still try to delete from database even if storage deletion failed
	}

	// Delete from database
	dbErr := s.db.OnFileDelete(fileID)
	if dbErr != nil {
		logging.LogError("Failed to delete file from database",
			logging.String("file_id", fileID),
			logging.Error(dbErr))

		// If storage deletion succeeded but database deletion failed,
		// we still consider it a partial failure
		if err == nil {
			return dbErr
		}
	}

	// If both operations failed, return the storage error as primary
	if err != nil {
		return err
	}

	logging.LogDebug("Successfully cleaned up file", logging.String("file_id", fileID))
	return nil
}
