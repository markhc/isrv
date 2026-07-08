package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"time"

	"cloud.google.com/go/auth/credentials"
	gcs "cloud.google.com/go/storage"
	"github.com/markhc/isrv/internal/models"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// GCSObjectMeta carries the object metadata applied when a writer is created.
type GCSObjectMeta struct {
	ContentType        string
	ContentDisposition string
	ChunkSizeBytes     int
}

// GCSReadResult is the outcome of opening a (range) reader on an object.
type GCSReadResult struct {
	Body        io.ReadCloser
	Size        int64
	Remain      int64
	StartOffset int64
	ContentType string
}

// GCSBucket is the subset of *gcs.BucketHandle behaviour used by GCSStorage,
// wrapped behind an interface so unit tests can substitute a mock.
type GCSBucket interface {
	Ping(ctx context.Context, prefix string) error
	StatObject(ctx context.Context, name string) error
	Upload(ctx context.Context, name string, meta GCSObjectMeta, r io.Reader) error
	NewRangeReader(ctx context.Context, name string, offset, length int64) (*GCSReadResult, error)
	DeleteObject(ctx context.Context, name string) error
	SignedURL(name string, opts *gcs.SignedURLOptions) (string, error)
}

// bucketHandleAdapter implements gcsBucket on top of the real SDK client.
type bucketHandleAdapter struct {
	bucket *gcs.BucketHandle
}

func (a bucketHandleAdapter) Ping(ctx context.Context, prefix string) error {
	it := a.bucket.Objects(ctx, &gcs.Query{Prefix: prefix})
	it.PageInfo().MaxSize = 1

	if _, err := it.Next(); err != nil && !errors.Is(err, iterator.Done) {
		return fmt.Errorf("list objects: %w", err)
	}

	return nil
}

func (a bucketHandleAdapter) StatObject(ctx context.Context, name string) error {
	if _, err := a.bucket.Object(name).Attrs(ctx); err != nil {
		return fmt.Errorf("stat object: %w", err)
	}

	return nil
}

func (a bucketHandleAdapter) Upload(ctx context.Context, name string, meta GCSObjectMeta, r io.Reader) error {
	// The writer gets its own cancellable context so we can abort uploads on error
	writeCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	writer := a.bucket.Object(name).NewWriter(writeCtx)
	writer.ContentType = meta.ContentType
	writer.ContentDisposition = meta.ContentDisposition

	if meta.ChunkSizeBytes > 0 {
		writer.ChunkSize = meta.ChunkSizeBytes
	}

	if _, err := io.Copy(writer, r); err != nil {
		cancel()
		_ = writer.Close()

		return fmt.Errorf("upload object: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("upload object: %w", err)
	}

	return nil
}

func (a bucketHandleAdapter) NewRangeReader(
	ctx context.Context,
	name string,
	offset, length int64,
) (*GCSReadResult, error) {
	r, err := a.bucket.Object(name).NewRangeReader(ctx, offset, length)
	if err != nil {
		return nil, fmt.Errorf("open object: %w", err)
	}

	return &GCSReadResult{
		Body:        r,
		Size:        r.Attrs.Size,
		Remain:      r.Remain(),
		StartOffset: r.Attrs.StartOffset,
		ContentType: r.Attrs.ContentType,
	}, nil
}

func (a bucketHandleAdapter) DeleteObject(ctx context.Context, name string) error {
	if err := a.bucket.Object(name).Delete(ctx); err != nil {
		return fmt.Errorf("delete object: %w", err)
	}

	return nil
}

func (a bucketHandleAdapter) SignedURL(name string, opts *gcs.SignedURLOptions) (string, error) {
	signedURL, err := a.bucket.SignedURL(name, opts)
	if err != nil {
		return "", fmt.Errorf("sign url: %w", err)
	}

	return signedURL, nil
}

type GCSStorage struct {
	Bucket         string
	BasePath       string
	ProxyDownloads bool
	ChunkSizeBytes int
	Client         GCSBucket
}

// NewGCSStorage creates a GCSStorage from the provided configuration and
// verifies bucket access. Credentials come from the configured service
// account key file, or Application Default Credentials when none is set.
func NewGCSStorage(ctx context.Context, config models.StorageConfiguration) (*GCSStorage, error) {
	var clientOpts []option.ClientOption

	if config.CredentialsFile != "" {
		creds, err := credentials.DetectDefault(&credentials.DetectOptions{
			CredentialsFile: config.CredentialsFile,
			Scopes:          []string{gcs.ScopeFullControl},
		})
		if err != nil {
			return nil, fmt.Errorf("load gcs credentials from %q: %w", config.CredentialsFile, err)
		}

		clientOpts = append(clientOpts, option.WithAuthCredentials(creds))
	}

	client, err := gcs.NewClient(ctx, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("create gcs client: %w", err)
	}

	storage := &GCSStorage{
		Bucket:         config.BucketName,
		BasePath:       config.BasePath,
		ProxyDownloads: config.ProxyDownloads,
		ChunkSizeBytes: config.UploadPartSizeMB * 1024 * 1024,
		Client:         bucketHandleAdapter{bucket: client.Bucket(config.BucketName)},
	}

	if err := storage.HealthCheck(ctx); err != nil {
		return nil, fmt.Errorf("access gcs bucket %q: %w", config.BucketName, err)
	}

	return storage, nil
}

// Backend returns the backend identifier ("gcs").
func (storage *GCSStorage) Backend() string { return BackendGCS }

// HealthCheck verifies the bucket is reachable and the credentials can list objects in it.
func (storage *GCSStorage) HealthCheck(ctx context.Context) error {
	if err := storage.Client.Ping(ctx, storage.BasePath); err != nil {
		return fmt.Errorf("list gcs bucket %q: %w", storage.Bucket, err)
	}

	return nil
}

// FileExists reports whether an object with the given ID exists in the bucket.
func (storage *GCSStorage) FileExists(ctx context.Context, fileID string) (bool, error) {
	var err error
	defer recordOpDuration(ctx, BackendGCS, OperationExists, time.Now(), &err)

	err = storage.Client.StatObject(ctx, storage.objectName(fileID))

	if err == nil {
		return true, nil
	}

	if errors.Is(err, gcs.ErrObjectNotExist) {
		// A missing object is not an error for this query.
		err = nil

		return false, nil
	}

	return false, fmt.Errorf("failed to check file existence: %w", err)
}

// Save uploads r to the GCS bucket and returns the object name
func (storage *GCSStorage) Save(
	ctx context.Context,
	fileID string,
	r io.Reader,
	opts SaveOptions,
) (string, error) {
	var err error
	defer recordOpDuration(ctx, BackendGCS, OperationSave, time.Now(), &err)

	meta := GCSObjectMeta{
		ContentType:    opts.ContentType,
		ChunkSizeBytes: storage.ChunkSizeBytes,
	}

	if opts.Filename != "" {
		sanitizedFileName := url.PathEscape(opts.Filename)
		meta.ContentDisposition = "inline; filename=\"" + sanitizedFileName + "\""
	}

	if err = storage.Client.Upload(ctx, storage.objectName(fileID), meta, r); err != nil {
		err = fmt.Errorf("failed to upload file to GCS: %w", err)

		return "", err
	}

	return fileID, nil
}

// DeleteFile removes the object with the given ID from the GCS bucket.
// A missing object is treated as already deleted.
func (storage *GCSStorage) DeleteFile(ctx context.Context, fileID string) error {
	var err error
	defer recordOpDuration(ctx, BackendGCS, OperationDelete, time.Now(), &err)

	err = storage.Client.DeleteObject(ctx, storage.objectName(fileID))
	if err != nil && !errors.Is(err, gcs.ErrObjectNotExist) {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	// Reset error so the deferred recordOpDuration call doesn't log a failure for a missing object.
	err = nil

	return nil
}

// Open fetches the object from GCS and returns a reader over its bytes. When
// brange is non-nil the range is resolved by GCS so partial downloads work.
// The caller must Close the returned Object.
func (storage *GCSStorage) Open(ctx context.Context, fileID string, brange *ByteRange) (*Object, error) {
	var err error
	defer recordOpDuration(ctx, BackendGCS, OperationServe, time.Now(), &err)

	offset, length := gcsRange(brange)

	object, err := storage.Client.NewRangeReader(ctx, storage.objectName(fileID), offset, length)
	if err != nil {
		if errors.Is(err, gcs.ErrObjectNotExist) {
			err = ErrObjectNotFound

			return nil, err
		}

		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) && apiErr.Code == http.StatusRequestedRangeNotSatisfiable {
			err = ErrInvalidRange

			return nil, err
		}

		err = fmt.Errorf("failed to fetch object: %w", err)

		return nil, err
	}

	result := &Object{
		Body:        object.Body,
		Size:        object.Size,
		Length:      object.Remain,
		ContentType: object.ContentType,
	}

	if brange != nil {
		result.Partial = true
		result.ContentRange = fmt.Sprintf(
			"bytes %d-%d/%d",
			object.StartOffset,
			object.StartOffset+object.Remain-1,
			object.Size,
		)
	}

	return result, nil
}

// PresignedURL generates a V4 signed GCS URL for direct client download. It
// reports ok == false in proxy mode, where the server streams the object via Open instead.
func (storage *GCSStorage) PresignedURL(ctx context.Context, file *models.File) (string, bool, error) {
	if storage.ProxyDownloads {
		return "", false, nil
	}

	sanitizedFileName := url.PathEscape(file.Name)

	contentType := "application/octet-stream"
	if file.ContentType != "" {
		contentType = file.ContentType
	}

	signedURL, err := storage.Client.SignedURL(storage.objectName(file.ID), &gcs.SignedURLOptions{
		Scheme:  gcs.SigningSchemeV4,
		Method:  http.MethodGet,
		Expires: time.Now().Add(12 * time.Hour),
		QueryParameters: url.Values{
			"response-content-disposition": {"inline; filename=\"" + sanitizedFileName + "\""},
			"response-content-type":        {contentType},
		},
	})
	if err != nil {
		return "", false, fmt.Errorf("failed to generate signed url: %w", err)
	}

	return signedURL, true, nil
}

// objectName maps a file ID to its object name under the configured prefix.
func (storage *GCSStorage) objectName(fileID string) string {
	return path.Join(storage.BasePath, fileID)
}

// gcsRange translates a ByteRange into the (offset, length) pair expected by
// the GCS range reader: a negative offset reads the last |offset| bytes and a
// negative length reads until EOF.
func gcsRange(brange *ByteRange) (int64, int64) {
	switch {
	case brange == nil:
		return 0, -1
	case brange.Suffix > 0:
		return -brange.Suffix, -1
	case brange.End < 0:
		return brange.Start, -1
	default:
		return brange.Start, brange.End - brange.Start + 1
	}
}
