package storage

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"path"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/headers"
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

// ServeFile delivers the file to the client. In proxy mode the object is
// streamed through the server; otherwise the client is redirected to a
// pre-signed S3 URL.
func (storage *S3Storage) ServeFile(
	c fiber.Ctx,
	file *models.File,
) error {
	if storage.ProxyDownloads {
		return storage.proxyFile(c, file)
	}

	return storage.redirectPresigned(c, file)
}

// proxyFile fetches the object from S3 and streams it to the client. Range
// requests are forwarded to S3 so partial downloads work without buffering
// the whole object.
func (storage *S3Storage) proxyFile(c fiber.Ctx, file *models.File) error {
	var err error
	defer recordOpDuration(c.Context(), BackendS3, OperationServe, time.Now(), &err)

	input := &s3.GetObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(path.Join(storage.BasePath, file.ID)),
	}

	if rangeHeader := c.Get(fiber.HeaderRange); rangeHeader != "" {
		input.Range = aws.String(rangeHeader)
	}

	var object *s3.GetObjectOutput
	object, err = storage.client.GetObject(c.Context(), input)
	if err != nil {
		var noSuchKey *types.NoSuchKey
		if errors.As(err, &noSuchKey) {
			return c.Status(fiber.StatusNotFound).SendString("not found")
		}

		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "InvalidRange" {
			return c.Status(fiber.StatusRequestedRangeNotSatisfiable).SendString("invalid range")
		}

		return c.Status(fiber.StatusInternalServerError).SendString("failed to fetch file")
	}

	contentType := file.ContentType
	if contentType == "" && object.ContentType != nil {
		contentType = *object.ContentType
	}

	if contentType == "" {
		contentType = "application/octet-stream"
	}

	headers.SetHeaders(c, file.Name, contentType, true, true)
	c.Set(fiber.HeaderAcceptRanges, "bytes")

	if object.ContentRange != nil {
		c.Set(fiber.HeaderContentRange, *object.ContentRange)
		c.Status(fiber.StatusPartialContent)
	}

	// SendStream closes the body once the response has been written.
	if object.ContentLength != nil {
		return c.SendStream(object.Body, int(*object.ContentLength))
	}

	return c.SendStream(object.Body)
}

// redirectPresigned generates a pre-signed S3 URL and redirects the client to it.
func (storage *S3Storage) redirectPresigned(c fiber.Ctx, file *models.File) error {
	sanitizedFileName := url.PathEscape(file.Name)
	objectKey := path.Join(storage.BasePath, file.ID)

	// cacheControl := "no-cache"
	cacheControl := "public, max-age=43200" // 12 hours

	// contentDisposition := "attachment"
	contentDisposition := "inline"

	contentType := "application/octet-stream"
	if file.ContentType != "" {
		contentType = file.ContentType
	}

	presignedUrl, err := storage.presigner.PresignGetObject(c.Context(), &s3.GetObjectInput{
		Bucket:                     aws.String(storage.Bucket),
		Key:                        aws.String(objectKey),
		ResponseCacheControl:       aws.String(cacheControl),
		ResponseContentDisposition: aws.String(contentDisposition + "; filename=\"" + sanitizedFileName + "\""),
		ResponseContentType:        aws.String(contentType),
	}, s3.WithPresignExpires(12*time.Hour))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).SendString("Failed to generate file URL")
	}

	return c.Redirect().Status(fiber.StatusFound).To(presignedUrl.URL)
}
