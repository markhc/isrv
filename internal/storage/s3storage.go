package storage

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/markhc/isrv/internal/models"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
)

// s3api is the subset of *s3.Client operations used by S3Storage.
type s3api interface {
	HeadBucket(
		ctx context.Context,
		params *s3.HeadBucketInput,
		optFns ...func(*s3.Options),
	) (*s3.HeadBucketOutput, error)
	HeadObject(
		ctx context.Context,
		params *s3.HeadObjectInput,
		optFns ...func(*s3.Options),
	) (*s3.HeadObjectOutput, error)
	GetObject(
		ctx context.Context,
		params *s3.GetObjectInput,
		optFns ...func(*s3.Options),
	) (*s3.GetObjectOutput, error)
	DeleteObject(
		ctx context.Context,
		params *s3.DeleteObjectInput,
		optFns ...func(*s3.Options),
	) (*s3.DeleteObjectOutput, error)
}

// s3presigner is the subset of *s3.PresignClient operations used by S3Storage.
type s3presigner interface {
	PresignGetObject(
		ctx context.Context,
		params *s3.GetObjectInput,
		optFns ...func(*s3.PresignOptions),
	) (*v4.PresignedHTTPRequest, error)
}

// s3uploader is the subset of *transfermanager.Client operations used by
// S3Storage. The transfer manager splits large objects into parts and uploads
// them concurrently.
type s3uploader interface {
	UploadObject(
		ctx context.Context,
		input *transfermanager.UploadObjectInput,
		opts ...func(*transfermanager.Options),
	) (*transfermanager.UploadObjectOutput, error)
}

// S3Storage implements the Storage interface using an S3-compatible object store.
type S3Storage struct {
	Bucket   string
	BasePath string
	// ProxyDownloads streams objects through the server on download instead
	// of redirecting the client to a pre-signed URL.
	ProxyDownloads bool

	client    s3api
	presigner s3presigner
	uploader  s3uploader
}

// newS3Options builds the S3 client options from the storage configuration.
// When no custom endpoint is configured, the SDK resolves the AWS endpoint
// from the region and uses virtual-hosted-style addressing. A custom endpoint
// (MinIO, GCS, ...) is used as-is with path-style addressing, which
// S3-compatible stores generally require.
func newS3Options(config models.StorageConfiguration) s3.Options {
	options := s3.Options{
		Region: config.Region,
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
			config.AccessKey,
			config.SecretKey,
			"",
		)),
	}

	if config.Endpoint != "" {
		options.BaseEndpoint = aws.String(config.Endpoint)
		options.UsePathStyle = true
	}

	// Register AWS SDK middleware that emits an OTel span per API call with
	// the standard rpc.system / rpc.service / rpc.method attributes.
	otelaws.AppendMiddlewares(&options.APIOptions)

	return options
}

// NewS3Storage creates an S3Storage from the provided configuration and verifies
// bucket access. It returns an error if the bucket cannot be reached.
func NewS3Storage(ctx context.Context, config models.StorageConfiguration) (*S3Storage, error) {
	awsClient := s3.New(newS3Options(config))

	// HeadBucket verifies connectivity without requiring any specific object to exist.
	_, err := awsClient.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(config.BucketName),
	})
	if err != nil {
		return nil, fmt.Errorf("access s3 bucket %q: %w", config.BucketName, err)
	}

	uploader := transfermanager.New(awsClient, func(options *transfermanager.Options) {
		if config.UploadPartSizeMB > 0 {
			options.PartSizeBytes = int64(config.UploadPartSizeMB) * 1024 * 1024
		}
		if config.UploadConcurrency > 0 {
			options.Concurrency = config.UploadConcurrency
		}
	})

	return &S3Storage{
		Bucket:         config.BucketName,
		BasePath:       config.BasePath,
		ProxyDownloads: config.ProxyDownloads,
		client:         awsClient,
		presigner:      s3.NewPresignClient(awsClient),
		uploader:       uploader,
	}, nil
}

// Backend returns the backend identifier ("s3").
func (storage *S3Storage) Backend() string { return BackendS3 }

// HealthCheck issues a HeadBucket against the configured S3 bucket to verify
// connectivity and that the bucket is still accessible.
func (storage *S3Storage) HealthCheck(ctx context.Context) error {
	if _, err := storage.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(storage.Bucket),
	}); err != nil {
		return fmt.Errorf("head bucket %q: %w", storage.Bucket, err)
	}

	return nil
}

// FileExists reports whether an object with the given ID exists in the S3 bucket.
func (storage *S3Storage) FileExists(ctx context.Context, fileID string) (bool, error) {
	var err error
	defer recordOpDuration(ctx, BackendS3, OperationExists, time.Now(), &err)

	_, err = storage.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(path.Join(storage.BasePath, fileID)),
	})

	if err == nil {
		return true, nil
	}

	var notFound *types.NotFound
	if isNotFound := errors.As(err, &notFound); isNotFound {
		// A missing object is not an error for this query.
		err = nil

		return false, nil
	}

	return false, fmt.Errorf("failed to check file existence: %w", err)
}

// SaveFileUpload uploads the file to the S3 bucket and returns the object key.
// Large objects are split into parts and uploaded concurrently by the S3
// transfer manager.
func (storage *S3Storage) SaveFileUpload(
	ctx context.Context,
	fileID string,
	file multipart.File,
	fileHeader *multipart.FileHeader,
) (string, error) {
	var err error
	defer recordOpDuration(ctx, BackendS3, OperationSave, time.Now(), &err)

	sanitizedFileName := url.PathEscape(fileHeader.Filename)
	contentDisposition := "inline; filename=\"" + sanitizedFileName + "\""

	_, err = storage.uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket:             aws.String(storage.Bucket),
		Key:                aws.String(path.Join(storage.BasePath, fileID)),
		Body:               file,
		ContentDisposition: aws.String(contentDisposition),
		ContentType:        aws.String(fileHeader.Header.Get("Content-Type")),
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload file to S3: %w", err)
	}

	return fileID, nil
}

// DeleteFile removes the object with the given ID from the S3 bucket.
func (storage *S3Storage) DeleteFile(ctx context.Context, fileID string) error {
	var err error
	defer recordOpDuration(ctx, BackendS3, OperationDelete, time.Now(), &err)

	_, err = storage.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(path.Join(storage.BasePath, fileID)),
	})
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

// Open fetches the object from S3 and returns a reader over its bytes. When
// brange is non-nil the range is forwarded to S3 so partial downloads work
// without buffering the whole object. The caller must Close the returned
// Object. Open maps a missing object to ErrObjectNotFound and an unsatisfiable
// range to ErrInvalidRange.
func (storage *S3Storage) Open(ctx context.Context, fileID string, brange *ByteRange) (*Object, error) {
	var err error
	defer recordOpDuration(ctx, BackendS3, OperationServe, time.Now(), &err)

	input := &s3.GetObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(path.Join(storage.BasePath, fileID)),
	}

	if brange != nil {
		input.Range = aws.String(brange.header())
	}

	object, err := storage.client.GetObject(ctx, input)
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			err = ErrObjectNotFound

			return nil, err
		}

		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidRange" {
			err = ErrInvalidRange

			return nil, err
		}

		err = fmt.Errorf("failed to fetch object: %w", err)

		return nil, err
	}

	result := &Object{Body: object.Body}

	if object.ContentType != nil {
		result.ContentType = *object.ContentType
	}

	if object.ContentLength != nil {
		result.Length = *object.ContentLength
	}

	if object.ContentRange != nil {
		result.Partial = true
		result.ContentRange = *object.ContentRange
		result.Size = parseContentRangeTotal(*object.ContentRange, result.Length)
	} else {
		result.Size = result.Length
	}

	return result, nil
}

// PresignedURL generates a pre-signed S3 URL for direct client download. It
// reports ok == false in proxy mode, where the server streams the object via
// Open instead.
func (storage *S3Storage) PresignedURL(ctx context.Context, file *models.File) (string, bool, error) {
	if storage.ProxyDownloads {
		return "", false, nil
	}

	sanitizedFileName := url.PathEscape(file.Name)
	objectKey := path.Join(storage.BasePath, file.ID)

	cacheControl := "public, max-age=43200" // 12 hours
	contentDisposition := "inline"

	contentType := "application/octet-stream"
	if file.ContentType != "" {
		contentType = file.ContentType
	}

	presignedURL, err := storage.presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket:                     aws.String(storage.Bucket),
		Key:                        aws.String(objectKey),
		ResponseCacheControl:       aws.String(cacheControl),
		ResponseContentDisposition: aws.String(contentDisposition + "; filename=\"" + sanitizedFileName + "\""),
		ResponseContentType:        aws.String(contentType),
	}, s3.WithPresignExpires(12*time.Hour))
	if err != nil {
		return "", false, fmt.Errorf("failed to generate presigned url: %w", err)
	}

	return presignedURL.URL, true, nil
}

// parseContentRangeTotal extracts the total object size from a Content-Range
// header value of the form "bytes start-end/total". It falls back to fallback
// when the value cannot be parsed (for example, an unknown total "*").
func parseContentRangeTotal(contentRange string, fallback int64) int64 {
	slash := strings.LastIndex(contentRange, "/")
	if slash < 0 {
		return fallback
	}

	total, err := strconv.ParseInt(strings.TrimSpace(contentRange[slash+1:]), 10, 64)
	if err != nil {
		return fallback
	}

	return total
}
