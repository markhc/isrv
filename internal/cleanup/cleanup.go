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

// cleanupDeleteSource labels FilesDeleted observations originating from the
// background cleanup loop.
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

// NewService returns a cleanup Service. When enabled is false, Start is a
// no-op and the service performs no work.
func NewService(db database.Database, storage storage.Storage, enabled bool, interval time.Duration) *Service {
	return &Service{
		db:       db,
		storage:  storage,
		enabled:  enabled,
		interval: interval,
	}
}

// Start launches the background cleanup goroutine and returns a cancel
// function that stops it. It is a no-op (returning nil) when the service is
// disabled.
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

// Join blocks until the background cleanup loop has finished any in-flight
// cycle. It should be called after the context passed to Start has been
// cancelled, to ensure a graceful shutdown.
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
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	err := s.storage.DeleteFile(ctx, fileID)
	if err != nil {
		logging.ErrorCtx(ctx, "failed to delete file from storage",
			logging.String("file_id", fileID),
			logging.Error(err))

		// Fall through and still attempt the database delete: leaving the
		// record in place after a transient storage error would prevent any
		// future retry from running, since the next cycle would treat the
		// file as already expired and re-queue the same deletion.
	}

	dbErr := s.db.OnFileDelete(ctx, fileID)
	if dbErr != nil {
		logging.ErrorCtx(ctx, "failed to delete file from database",
			logging.String("file_id", fileID),
			logging.Error(dbErr))

		// Storage succeeded but the database delete failed: report a partial
		// failure so the cycle metrics reflect the inconsistency.
		if err == nil {
			telemetry.FilesDeleted.Add(ctx, 1, metric.WithAttributes(
				cleanupDeleteSource,
				attribute.String(telemetry.AttrResult, telemetry.ResultError),
			))

			return fmt.Errorf("failed to delete file from database: %w", dbErr)
		}
	}

	// When both deletes failed, surface the storage error as the primary
	// cause; the database error has already been logged above.
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
