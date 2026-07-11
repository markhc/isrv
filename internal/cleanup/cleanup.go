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
	"go.opentelemetry.io/otel/metric"
)

// cleanupDeleteSource labels FilesDeleted observations originating from the
// background cleanup loop.
var cleanupDeleteSource = attribute.String(telemetry.AttrSource, "cleanup")

// CycleLocker serializes cleanup cycles across replicas. TryLock attempts to
// acquire the cluster-wide cleanup lock without blocking: on success it
// returns acquired=true and a release function that must be called once the
// cycle finishes; when another replica holds the lock it returns
// acquired=false with a nil release and a nil error.
type CycleLocker interface {
	TryLock(ctx context.Context) (func(), bool, error)
}

// nopCycleLocker always grants the lock. It preserves single-replica
// behavior when clustering is disabled.
type nopCycleLocker struct{}

func (nopCycleLocker) TryLock(context.Context) (func(), bool, error) {
	return func() {}, true, nil
}

// Service periodically scans for expired files and removes them from both
// storage and the database.
type Service struct {
	db       database.Database
	storage  storage.Storage
	locker   CycleLocker
	interval time.Duration
	enabled  bool

	wg sync.WaitGroup
}

// NewService returns a cleanup Service. When enabled is false, Start is a
// no-op and the service performs no work. A nil locker means uncoordinated
// (single-replica) operation: every cycle runs unconditionally.
func NewService(
	db database.Database,
	storage storage.Storage,
	enabled bool,
	interval time.Duration,
	locker CycleLocker,
) *Service {
	if locker == nil {
		locker = nopCycleLocker{}
	}

	return &Service{
		db:       db,
		storage:  storage,
		locker:   locker,
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
			s.runCleanupCycle(ctx)
		}
	}
}

// runCleanupCycle acquires the cluster-wide cycle lock and performs one
// cleanup pass while holding it.
func (s *Service) runCleanupCycle(ctx context.Context) {
	release, acquired, err := s.locker.TryLock(ctx)
	if err != nil {
		logging.ErrorCtx(ctx, "failed to acquire cleanup cycle lock, skipping cycle", logging.Error(err))

		return
	}

	if !acquired {
		logging.DebugCtx(ctx, "cleanup cycle lock held by another replica, skipping cycle")

		return
	}

	defer release()

	s.performCleanup(ctx)
}

func (s *Service) performCleanup(ctx context.Context) {
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
		logging.ErrorCtx(ctx, "failed to get expired files", logging.Error(err))
		result = telemetry.ResultError

		return
	}

	if len(expiredFiles) == 0 {
		logging.DebugCtx(ctx, "no expired files found")

		return
	}

	logging.InfoCtx(ctx, "found expired files", logging.Int("count", len(expiredFiles)))

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
