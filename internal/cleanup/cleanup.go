package cleanup

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/storage"
	"github.com/markhc/isrv/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
)

// cleanupDeleteSource labels FilesDeleted observations originating from the cleanup loop.
var cleanupDeleteSource = attribute.String(telemetry.AttrSource, "cleanup")

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
		logging.InfoCtx(ctx, "file cleanup service is disabled")

		return nil
	}

	cancellableCtx, cancel := context.WithCancel(ctx)
	s.wg.Go(func() { s.cleanupLoop(cancellableCtx) })

	logging.InfoCtx(ctx, "file cleanup service started", logging.String("interval", s.interval.String()))

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

//nolint:funlen // Linear cleanup-cycle code path with inline metric emission.
func (s *Service) performCleanup(ctx context.Context) {
	ctx, span := telemetry.Tracer().Start(ctx, "cleanup.cycle")
	defer span.End()

	start := time.Now()
	result := telemetry.ResultSuccess

	defer func() {
		telemetry.CleanupCycleDuration.Record(ctx, time.Since(start).Seconds(),
			metric.WithAttributes(attribute.String(telemetry.AttrResult, result)),
		)
	}()

	logging.DebugCtx(ctx, "starting cleanup cycle")

	expiredFiles, err := s.db.GetExpiredFiles(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get expired files")
		logging.ErrorCtx(ctx, "failed to get expired files", logging.Error(err))
		result = telemetry.ResultError

		return
	}

	if len(expiredFiles) == 0 {
		logging.DebugCtx(ctx, "no expired files found")

		return
	}

	logging.InfoCtx(ctx, "found expired files", logging.Int("count", len(expiredFiles)))
	span.SetAttributes(attribute.Int(telemetry.AttrCleanupExpiredCount, len(expiredFiles)))

	successCount := 0
	failureCount := 0

	for _, fileID := range expiredFiles {
		if err := s.cleanupFile(ctx, fileID); err != nil {
			logging.ErrorCtx(ctx, "failed to cleanup file",
				logging.String("file_id", fileID),
				logging.Error(err))
			failureCount++
		} else {
			successCount++
		}
	}

	if successCount > 0 {
		telemetry.CleanupFilesProcessed.Add(ctx, int64(successCount), metric.WithAttributes(
			attribute.String(telemetry.AttrResult, telemetry.ResultSuccess),
		))
	}

	if failureCount > 0 {
		telemetry.CleanupFilesProcessed.Add(ctx, int64(failureCount), metric.WithAttributes(
			attribute.String(telemetry.AttrResult, telemetry.ResultError),
		))
		result = telemetry.ResultError
	}

	logging.InfoCtx(ctx, "cleanup cycle completed",
		logging.Int("success", successCount),
		logging.Int("failures", failureCount))
}

func (s *Service) cleanupFile(ctx context.Context, fileID string) error {
	// Create a context with timeout for the storage operation
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := s.storage.DeleteFile(ctx, fileID)
	if err != nil {
		logging.ErrorCtx(ctx, "failed to delete file from storage",
			logging.String("file_id", fileID),
			logging.Error(err))

		// Still try to delete from database even if storage deletion failed
	}

	// Delete from database
	dbErr := s.db.OnFileDelete(ctx, fileID)
	if dbErr != nil {
		logging.ErrorCtx(ctx, "failed to delete file from database",
			logging.String("file_id", fileID),
			logging.Error(dbErr))

		// If storage deletion succeeded but database deletion failed,
		// we still consider it a partial failure
		if err == nil {
			telemetry.FilesDeleted.Add(ctx, 1, metric.WithAttributes(
				cleanupDeleteSource,
				attribute.String(telemetry.AttrResult, telemetry.ResultError),
			))

			return fmt.Errorf("failed to delete file from database: %w", dbErr)
		}
	}

	// If both operations failed, return the storage error as primary
	if err != nil {
		telemetry.FilesDeleted.Add(ctx, 1, metric.WithAttributes(
			cleanupDeleteSource,
			attribute.String(telemetry.AttrResult, telemetry.ResultError),
		))

		return fmt.Errorf("failed to delete file from storage: %w", err)
	}

	telemetry.FilesDeleted.Add(ctx, 1, metric.WithAttributes(
		cleanupDeleteSource,
		attribute.String(telemetry.AttrResult, telemetry.ResultSuccess),
	))

	logging.DebugCtx(ctx, "successfully cleaned up file", logging.String("file_id", fileID))

	return nil
}
