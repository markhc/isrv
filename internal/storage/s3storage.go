package storage

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/markhc/isrv/internal/models"
)

// s3api is the subset of *s3.Client operations used by S3Storage.
type s3api interface {
	HeadObject(
		ctx context.Context,
		params *s3.HeadObjectInput,
		optFns ...func(*s3.Options),
	) (*s3.HeadObjectOutput, error)
	PutObject(
		ctx context.Context,
		params *s3.PutObjectInput,
		optFns ...func(*s3.Options),
	) (*s3.PutObjectOutput, error)
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

// S3Storage implements the Storage interface using an S3-compatible object store.
type S3Storage struct {
	Endpoint  string
	Bucket    string
	Region    string
	BasePath  string
	client    s3api
	presigner s3presigner
}

// NewS3Storage creates an S3Storage from the provided configuration and verifies
// bucket access. It panics if the bucket cannot be reached.
func NewS3Storage(ctx context.Context, config models.StorageConfiguration) *S3Storage {
	options := s3.Options{
		Region: config.Region,
		Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
			config.AccessKey,
			config.SecretKey,
			"",
		)),
		UsePathStyle: true,
		BaseEndpoint: aws.String(config.Endpoint),
	}

	awsClient := s3.New(options)

	// Test bucket access with HeadBucket instead of HeadObject
	// This verifies connectivity without requiring a specific object to exist
	_, err := awsClient.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(config.BucketName),
	})
	if err != nil {
		panic("Failed to access S3 bucket: " + err.Error())
	}

	return &S3Storage{
		Endpoint:  config.Endpoint,
		Bucket:    config.BucketName,
		Region:    config.Region,
		BasePath:  config.BasePath,
		client:    awsClient,
		presigner: s3.NewPresignClient(awsClient),
	}
}

// FileExists reports whether an object with the given ID exists in the S3 bucket.
func (storage *S3Storage) FileExists(ctx context.Context, fileID string) (bool, error) {
	_, err := storage.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(path.Join(storage.BasePath, fileID)),
	})

	if err == nil {
		return true, nil
	}

	var notFound *types.NotFound
	if isNotFound := errors.As(err, &notFound); isNotFound {
		// the object does not exist. Don't propagate this as an error.
		return false, nil
	}

	return false, fmt.Errorf("failed to check file existence: %w", err)
}

// SaveFileUpload uploads the file to the S3 bucket and returns the object key.
func (storage *S3Storage) SaveFileUpload(
	ctx context.Context,
	fileID string,
	file multipart.File,
	fileHeader *multipart.FileHeader,
) (string, error) {
	sanitizedFileName := url.PathEscape(fileHeader.Filename)
	contentDisposition := "inline; filename=\"" + sanitizedFileName + "\""

	_, err := storage.client.PutObject(ctx, &s3.PutObjectInput{
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
	_, err := storage.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(path.Join(storage.BasePath, fileID)),
	})
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

// ServeFile generates a pre-signed S3 URL and redirects the client to it.
func (storage *S3Storage) ServeFile(
	w http.ResponseWriter,
	r *http.Request,
	fileID string,
	fileName string,
	metadata map[string]string,
	inlineContent bool,
	cachingEnabled bool,
) {
	sanitizedFileName := url.PathEscape(fileName)
	objectKey := path.Join(storage.BasePath, fileID)

	cacheControl := "no-cache"
	if cachingEnabled {
		cacheControl = "public, max-age=43200" // Cache for 12 hours
	}

	contentDisposition := "attachment"
	if inlineContent {
		contentDisposition = "inline"
	}

	contentType := "application/octet-stream"
	if ct, ok := metadata["Content-Type"]; ok {
		contentType = ct
	}

	presignedUrl, err := storage.presigner.PresignGetObject(r.Context(), &s3.GetObjectInput{
		Bucket:                     aws.String(storage.Bucket),
		Key:                        aws.String(objectKey),
		ResponseCacheControl:       aws.String(cacheControl),
		ResponseContentDisposition: aws.String(contentDisposition + "; filename=\"" + sanitizedFileName + "\""),
		ResponseContentType:        aws.String(contentType),
	}, s3.WithPresignExpires(12*time.Hour)) // URL valid for 12 hours
	if err != nil {
		http.Error(w, "Failed to generate file URL", http.StatusInternalServerError)

		return
	}

	http.Redirect(w, r, presignedUrl.URL, http.StatusFound)
}
