package storage

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/markhc/isrv/internal/models"
)

// LocalStorage implements the Storage interface for local filesystem storage
type S3Storage struct {
	Endpoint string
	Bucket   string
	Region   string
	BasePath string
	Client   *s3.Client
}

func NewS3Storage(config models.StorageConfiguration) *S3Storage {
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
	_, err := awsClient.HeadBucket(context.Background(), &s3.HeadBucketInput{
		Bucket: aws.String(config.BucketName),
	})

	if err != nil {
		panic("Failed to access S3 bucket: " + err.Error())
	}

	return &S3Storage{
		Endpoint: config.Endpoint,
		Bucket:   config.BucketName,
		Region:   config.Region,
		BasePath: config.BasePath,
		Client:   awsClient,
	}
}

func (storage *S3Storage) FileExists(ctx context.Context, fileID string) (bool, error) {
	_, err := storage.Client.HeadObject(ctx, &s3.HeadObjectInput{
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
	return false, err
}

func (storage *S3Storage) SaveFileUpload(ctx context.Context, fileID string, file multipart.File, fileHeader *multipart.FileHeader) (string, error) {
	sanitizedFileName := url.PathEscape(fileHeader.Filename)
	contentDisposition := "inline; filename=\"" + sanitizedFileName + "\""

	_, err := storage.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:             aws.String(storage.Bucket),
		Key:                aws.String(path.Join(storage.BasePath, fileID)),
		Body:               file,
		ContentDisposition: aws.String(contentDisposition),
		ContentType:        aws.String(fileHeader.Header.Get("Content-Type")),
	})

	if err != nil {
		return "", err
	}

	return fileID, nil
}
func (storage *S3Storage) RetrieveFile(ctx context.Context, fileID string) ([]byte, error) {
	output, err := storage.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(path.Join(storage.BasePath, fileID)),
	})

	if err != nil {
		return nil, err
	}
	defer output.Body.Close()

	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(output.Body)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (storage *S3Storage) DeleteFile(ctx context.Context, fileID string) error {
	_, err := storage.Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(path.Join(storage.BasePath, fileID)),
	})

	return err
}

func (storage *S3Storage) ServeFile(w http.ResponseWriter, r *http.Request, fileID string, fileName string, metadata map[string]string, inlineContent bool, cachingEnabled bool) {
	objectKey := path.Join(storage.BasePath, fileID)
	presignClient := s3.NewPresignClient(storage.Client)

	cacheControl := "no-cache"
	if cachingEnabled {
		cacheControl = "public, max-age=39600" // Cache for 12 hours
	}

	contentDisposition := "attachment"
	if inlineContent {
		contentDisposition = "inline"
	}

	contentType := "application/octet-stream"
	if ct, ok := metadata["Content-Type"]; ok {
		contentType = ct
	}

	presignedUrl, err := presignClient.PresignGetObject(r.Context(), &s3.GetObjectInput{
		Bucket:                     aws.String(storage.Bucket),
		Key:                        aws.String(objectKey),
		ResponseCacheControl:       aws.String(cacheControl),
		ResponseContentDisposition: aws.String(contentDisposition + "; filename=\"" + fileName + "\""),
		ResponseContentType:        aws.String(contentType),
	}, s3.WithPresignExpires(12*time.Hour)) // URL valid for 12 hours

	if err != nil {
		http.Error(w, "Failed to generate file URL", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, presignedUrl.URL, http.StatusFound)
}
