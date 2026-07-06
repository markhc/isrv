package storage

//go:generate go tool mockery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ErrObjectNotFound is returned by Open when the requested object does not
// exist in the storage backend.
var ErrObjectNotFound = errors.New("storage: object not found")

// ErrInvalidRange is returned by Open when the requested byte range cannot be
// satisfied for the stored object (for example, a start offset beyond EOF).
var ErrInvalidRange = errors.New("storage: invalid byte range")

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
	OperationServe  = "serve"
)

// recordOpDuration emits an isrv.storage.operation.duration observation for
// the operation that started at start. It is intended to be deferred at the
// top of a storage method:
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

// SaveOptions carries optional object metadata for Save.
type SaveOptions struct {
	// Size is the number of bytes r will yield, or -1 when unknown.
	Size int64
	// ContentType to attach to the stored object, when the backend supports
	// object metadata.
	ContentType string
	// Filename for the backend's content-disposition metadata, if any.
	Filename string
}

// Storage is the interface for file storage backends.
type Storage interface {
	// Backend returns a short, stable identifier for this storage backend
	// (e.g. "local", "s3") suitable for use as a metric attribute value.
	Backend() string
	// HealthCheck verifies the storage backend is reachable. It is intended
	// for readiness probes and must be cheap and respect ctx cancellation.
	HealthCheck(ctx context.Context) error
	// FileExists reports whether a file with the given ID exists in storage.
	FileExists(ctx context.Context, fileID string) (bool, error)
	// Save writes r to storage under fileID and returns its backend-specific
	// storage path or key. opts carries optional object metadata; a Size of
	// -1 signals an unknown-length reader (e.g. an encrypting stream).
	Save(ctx context.Context, fileID string, r io.Reader, opts SaveOptions) (string, error)
	// DeleteFile removes the file with the given ID from storage.
	DeleteFile(ctx context.Context, fileID string) error
	// Open returns a reader over the stored object's bytes. When brange is
	// non-nil the reader is positioned to serve only that byte range and the
	// returned Object carries the partial-content metadata. The caller owns
	// the returned Object and must Close it. Open returns ErrObjectNotFound
	// when the object does not exist and ErrInvalidRange when brange cannot be
	// satisfied.
	Open(ctx context.Context, fileID string, brange *ByteRange) (*Object, error)
	// PresignedURL returns a URL the client can use to download the object
	// directly from the backend, bypassing the server. ok is false when the
	// backend cannot offload delivery (local storage, or S3 in proxy mode),
	// in which case the caller must stream the bytes via Open.
	PresignedURL(ctx context.Context, file *models.File) (url string, ok bool, err error)
}

// ByteRange is a parsed single HTTP byte range. Exactly one form is expressed:
//
//   - [Start, End] when Suffix == 0 and End >= 0 (inclusive bounds)
//   - [Start, EOF] when Suffix == 0 and End < 0
//   - the last Suffix bytes when Suffix > 0 (Start and End are ignored)
//
// Backends resolve the range against the actual object size.
type ByteRange struct {
	Start  int64
	End    int64
	Suffix int64
}

// header renders the range as an HTTP Range request header value
// (e.g. "bytes=0-1023", "bytes=1024-", "bytes=-500").
func (r ByteRange) header() string {
	switch {
	case r.Suffix > 0:
		return fmt.Sprintf("bytes=-%d", r.Suffix)
	case r.End < 0:
		return fmt.Sprintf("bytes=%d-", r.Start)
	default:
		return fmt.Sprintf("bytes=%d-%d", r.Start, r.End)
	}
}

// Object is the result of opening a stored object for reading. Body must be
// closed by the caller once the response has been written.
type Object struct {
	// Body carries the object's bytes (the full object, or a single range
	// when a partial read was requested).
	Body io.ReadCloser
	// Size is the total size of the stored object in bytes.
	Size int64
	// Length is the number of bytes available in Body. It equals Size for a
	// full read and the range length for a partial read.
	Length int64
	// ContentType is the backend-detected content type, if any. It may be
	// empty, in which case the caller falls back to stored metadata.
	ContentType string
	// Partial reports whether Body holds a byte range rather than the whole
	// object.
	Partial bool
	// ContentRange is the value for the Content-Range response header. It is
	// set only when Partial is true (e.g. "bytes 0-1023/4096").
	ContentRange string
}
