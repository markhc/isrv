package storage

//go:generate go tool mockery

import (
	"context"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/markhc/isrv/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Backend identifiers used by Storage.Backend.
const (
	BackendLocal = "local"
	BackendS3    = "s3"
)

// Operation identifiers for the storage.operation.duration histogram.
const (
	OperationSave   = "save"
	OperationDelete = "delete"
	OperationExists = "exists"
)

// recordOpDuration emits an isrv.storage.operation.duration observation for the
// operation that started at start. It is intended to be deferred at the top of
// a storage method:
//
//	defer recordOpDuration(ctx, backend, OperationSave, time.Now(), &err)
func recordOpDuration(ctx context.Context, backend, op string, start time.Time, errPtr *error) {
	result := telemetry.ResultSuccess
	if errPtr != nil && *errPtr != nil {
		result = telemetry.ResultError
	}

	telemetry.StorageOpDuration.Record(ctx, time.Since(start).Seconds(),
		metric.WithAttributes(
			attribute.String(telemetry.AttrStorage, backend),
			attribute.String(telemetry.AttrOperation, op),
			attribute.String(telemetry.AttrResult, result),
		),
	)
}

// Storage is the interface for file storage backends.
type Storage interface {
	// Backend returns a short, stable identifier for this storage backend
	// (e.g. "local", "s3") suitable for use as a metric attribute value.
	Backend() string
	// HealthCheck verifies the storage backend is reachable. Intended for
	// readiness probes; should be cheap and respect ctx cancellation.
	HealthCheck(ctx context.Context) error
	// FileExists reports whether a file with the given ID exists in storage.
	FileExists(ctx context.Context, fileID string) (bool, error)
	// SaveFileUpload writes an uploaded file to storage and returns its storage path.
	SaveFileUpload(
		ctx context.Context,
		fileID string,
		file multipart.File,
		fileHeader *multipart.FileHeader) (string, error)
	// DeleteFile removes the file with the given ID from storage.
	DeleteFile(ctx context.Context, fileID string) error
	// ServeFile writes the file to the HTTP response, applying appropriate headers.
	ServeFile(
		w http.ResponseWriter,
		r *http.Request,
		fileID string,
		fileName string,
		metadata map[string]string,
		inlineContent bool,
		cachingEnabled bool)
}
