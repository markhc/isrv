package storage

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
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

func (storage *S3Storage) FileExists(fileID string) (bool, error) {
	_, err := storage.Client.HeadObject(context.Background(), &s3.HeadObjectInput{
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

func (storage *S3Storage) SaveFileUpload(fileID string, file multipart.File, fileHeader *multipart.FileHeader) (string, error) {
	_, err := storage.Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:             aws.String(storage.Bucket),
		Key:                aws.String(path.Join(storage.BasePath, fileID)),
		Body:               file,
		ContentDisposition: aws.String("inline; filename=\"" + fileHeader.Filename + "\""),
		ContentType:        aws.String(fileHeader.Header.Get("Content-Type")),
	})

	if err != nil {
		return "", err
	}

	return fileID, nil
}
func (storage *S3Storage) RetrieveFile(fileID string) ([]byte, error) {
	output, err := storage.Client.GetObject(context.Background(), &s3.GetObjectInput{
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

func (storage *S3Storage) DeleteFile(fileID string) error {
	_, err := storage.Client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(path.Join(storage.BasePath, fileID)),
	})

	return err
}

func (storage *S3Storage) ServeFile(w http.ResponseWriter, r *http.Request, fileID string, fileName string, metadata map[string]string, inlineContent bool, cachingEnabled bool) {
	objectKey := path.Join(storage.BasePath, fileID)
	presignClient := s3.NewPresignClient(storage.Client)

	presignedUrl, err := presignClient.PresignGetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(storage.Bucket),
		Key:    aws.String(objectKey),
	}, s3.WithPresignExpires(12*time.Hour)) // URL valid for 12 hours

	if err != nil {
		http.Error(w, "Failed to generate file URL", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, presignedUrl.URL, http.StatusFound)
}
