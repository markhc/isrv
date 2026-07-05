package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---- mock types ----

type MockS3Client struct{ mock.Mock }

func (m *MockS3Client) HeadBucket(_ context.Context, params *s3.HeadBucketInput, _ ...func(*s3.Options)) (*s3.HeadBucketOutput, error) {
	args := m.Called(params)
	out, _ := args.Get(0).(*s3.HeadBucketOutput)
	return out, args.Error(1)
}

func (m *MockS3Client) HeadObject(_ context.Context, params *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	args := m.Called(params)
	out, _ := args.Get(0).(*s3.HeadObjectOutput)
	return out, args.Error(1)
}

func (m *MockS3Client) PutObject(_ context.Context, params *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	args := m.Called(params)
	out, _ := args.Get(0).(*s3.PutObjectOutput)
	return out, args.Error(1)
}

func (m *MockS3Client) GetObject(_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	args := m.Called(params)
	out, _ := args.Get(0).(*s3.GetObjectOutput)
	return out, args.Error(1)
}

func (m *MockS3Client) DeleteObject(_ context.Context, params *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	args := m.Called(params)
	out, _ := args.Get(0).(*s3.DeleteObjectOutput)
	return out, args.Error(1)
}

type MockS3Presigner struct{ mock.Mock }

func (m *MockS3Presigner) PresignGetObject(_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	args := m.Called(params)
	req, _ := args.Get(0).(*v4.PresignedHTTPRequest)
	return req, args.Error(1)
}

type MockS3Uploader struct{ mock.Mock }

func (m *MockS3Uploader) UploadObject(_ context.Context, input *transfermanager.UploadObjectInput, _ ...func(*transfermanager.Options)) (*transfermanager.UploadObjectOutput, error) {
	args := m.Called(input)
	out, _ := args.Get(0).(*transfermanager.UploadObjectOutput)
	return out, args.Error(1)
}

// ---- test helpers ----

func newTestS3Storage(client s3api, presigner s3presigner, uploader s3uploader) *S3Storage {
	return &S3Storage{
		Bucket:    "test-bucket",
		BasePath:  "files",
		client:    client,
		presigner: presigner,
		uploader:  uploader,
	}
}

type testMultipartFile struct {
	*bytes.Reader
}

func (f *testMultipartFile) Close() error { return nil }

func newTestMultipartFile(content []byte) multipart.File {
	return &testMultipartFile{Reader: bytes.NewReader(content)}
}

// ---- tests ----

func Test_S3Storage_FileExists(t *testing.T) {
	tests := []struct {
		name       string
		headErr    error
		wantExists bool
		wantErr    bool
	}{
		{name: "object exists", wantExists: true},
		{name: "object not found", headErr: &types.NotFound{}},
		{name: "other error", headErr: errors.New("connection refused"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockS3Client{}
			client.On("HeadObject", mock.MatchedBy(func(p *s3.HeadObjectInput) bool {
				return p.Key != nil && *p.Key == "files/test-id"
			})).Return((*s3.HeadObjectOutput)(nil), tt.headErr)

			s := newTestS3Storage(client, nil, nil)
			got, err := s.FileExists(context.Background(), "test-id")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantExists, got)
			}
			client.AssertExpectations(t)
		})
	}
}

func Test_S3Storage_SaveFileUpload(t *testing.T) {
	tests := []struct {
		name    string
		putErr  error
		wantErr bool
	}{
		{name: "success"},
		{name: "put error", putErr: errors.New("write failed"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uploader := &MockS3Uploader{}
			uploader.On("UploadObject", mock.MatchedBy(func(p *transfermanager.UploadObjectInput) bool {
				return p.Key != nil && *p.Key == "files/test-id" &&
					p.ContentType != nil && *p.ContentType == "text/plain"
			})).Return((*transfermanager.UploadObjectOutput)(nil), tt.putErr)

			s := newTestS3Storage(nil, nil, uploader)
			header := &multipart.FileHeader{
				Filename: "test.txt",
				Header:   textproto.MIMEHeader{"Content-Type": []string{"text/plain"}},
			}
			gotID, err := s.SaveFileUpload(context.Background(), "test-id", newTestMultipartFile([]byte("hello")), header)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "test-id", gotID)
			}
			uploader.AssertExpectations(t)
		})
	}
}

func Test_S3Storage_DeleteFile(t *testing.T) {
	tests := []struct {
		name      string
		deleteErr error
		wantErr   bool
	}{
		{name: "success"},
		{name: "delete error", deleteErr: errors.New("forbidden"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockS3Client{}
			client.On("DeleteObject", mock.MatchedBy(func(p *s3.DeleteObjectInput) bool {
				return p.Key != nil && *p.Key == "files/test-id"
			})).Return((*s3.DeleteObjectOutput)(nil), tt.deleteErr)

			s := newTestS3Storage(client, nil, nil)
			err := s.DeleteFile(context.Background(), "test-id")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			client.AssertExpectations(t)
		})
	}
}

func Test_S3Storage_ServeFile(t *testing.T) {
	tests := []struct {
		name           string
		file           models.File
		metadata       map[string]string
		inlineContent  bool
		cachingEnabled bool
		presignErr     error
		presignURL     string
		wantStatus     int
		wantLocation   string
	}{
		{
			name: "attachment no cache",
			file: models.File{
				ID:   "test-id",
				Name: "file.bin",
			},
			metadata:     map[string]string{},
			presignURL:   "https://s3.example.com/presigned",
			wantStatus:   http.StatusFound,
			wantLocation: "https://s3.example.com/presigned",
		},
		{
			name: "inline with cache and custom content type",
			file: models.File{
				ID:          "test-id",
				Name:        "image.png",
				ContentType: "image/png",
			},
			inlineContent:  true,
			cachingEnabled: true,
			presignURL:     "https://s3.example.com/presigned-img",
			wantStatus:     http.StatusFound,
			wantLocation:   "https://s3.example.com/presigned-img",
		},
		{
			name: "presign error returns 500",
			file: models.File{
				ID:   "test-id",
				Name: "file.bin",
			},
			metadata:   map[string]string{},
			presignErr: errors.New("sign failed"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var returnReq *v4.PresignedHTTPRequest
			if tt.presignErr == nil {
				returnReq = &v4.PresignedHTTPRequest{URL: tt.presignURL, Method: http.MethodGet}
			}
			presigner := &MockS3Presigner{}
			presigner.On("PresignGetObject", mock.MatchedBy(func(p *s3.GetObjectInput) bool {
				return p.Key != nil && *p.Key == "files/test-id"
			})).Return(returnReq, tt.presignErr)

			s := newTestS3Storage(nil, presigner, nil)

			app := fiber.New()
			app.Get("/", func(c fiber.Ctx) error {
				return s.ServeFile(c, &tt.file)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			resp, err := app.Test(req)
			require.NoError(t, err)
			resp.Body.Close()

			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			if tt.wantLocation != "" {
				assert.Equal(t, tt.wantLocation, resp.Header.Get("Location"))
			}
			presigner.AssertExpectations(t)
		})
	}
}

type invalidRangeError struct{}

func (e *invalidRangeError) Error() string     { return "InvalidRange: range not satisfiable" }
func (e *invalidRangeError) ErrorCode() string { return "InvalidRange" }
func (e *invalidRangeError) ErrorMessage() string {
	return "The requested range is not satisfiable"
}
func (e *invalidRangeError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func Test_S3Storage_ServeFile_Proxy(t *testing.T) {
	content := []byte("hello proxy world")

	tests := []struct {
		name            string
		file            models.File
		rangeHeader     string
		output          *s3.GetObjectOutput
		getErr          error
		wantStatus      int
		wantBody        string
		wantContentType string
		wantRange       string
	}{
		{
			name: "full download",
			file: models.File{ID: "test-id", Name: "file.txt", ContentType: "text/plain"},
			output: &s3.GetObjectOutput{
				Body:          io.NopCloser(bytes.NewReader(content)),
				ContentLength: aws.Int64(int64(len(content))),
			},
			wantStatus:      http.StatusOK,
			wantBody:        string(content),
			wantContentType: "text/plain",
		},
		{
			name: "content type falls back to object metadata",
			file: models.File{ID: "test-id", Name: "file.bin"},
			output: &s3.GetObjectOutput{
				Body:          io.NopCloser(bytes.NewReader(content)),
				ContentLength: aws.Int64(int64(len(content))),
				ContentType:   aws.String("application/pdf"),
			},
			wantStatus:      http.StatusOK,
			wantBody:        string(content),
			wantContentType: "application/pdf",
		},
		{
			name:        "range request returns partial content",
			file:        models.File{ID: "test-id", Name: "file.txt", ContentType: "text/plain"},
			rangeHeader: "bytes=0-4",
			output: &s3.GetObjectOutput{
				Body:          io.NopCloser(bytes.NewReader(content[:5])),
				ContentLength: aws.Int64(5),
				ContentRange:  aws.String("bytes 0-4/17"),
			},
			wantStatus:      http.StatusPartialContent,
			wantBody:        string(content[:5]),
			wantContentType: "text/plain",
			wantRange:       "bytes 0-4/17",
		},
		{
			name:       "missing object returns 404",
			file:       models.File{ID: "test-id", Name: "file.txt"},
			getErr:     &types.NoSuchKey{},
			wantStatus: http.StatusNotFound,
		},
		{
			name:        "invalid range returns 416",
			file:        models.File{ID: "test-id", Name: "file.txt"},
			rangeHeader: "bytes=999-",
			getErr:      &invalidRangeError{},
			wantStatus:  http.StatusRequestedRangeNotSatisfiable,
		},
		{
			name:       "other error returns 500",
			file:       models.File{ID: "test-id", Name: "file.txt"},
			getErr:     errors.New("connection refused"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockS3Client{}
			client.On("GetObject", mock.MatchedBy(func(p *s3.GetObjectInput) bool {
				if p.Key == nil || *p.Key != "files/test-id" {
					return false
				}
				if tt.rangeHeader == "" {
					return p.Range == nil
				}
				return p.Range != nil && *p.Range == tt.rangeHeader
			})).Return(tt.output, tt.getErr)

			s := newTestS3Storage(client, nil, nil)
			s.ProxyDownloads = true

			app := fiber.New()
			app.Get("/", func(c fiber.Ctx) error {
				return s.ServeFile(c, &tt.file)
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.rangeHeader != "" {
				req.Header.Set("Range", tt.rangeHeader)
			}

			resp, err := app.Test(req)
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			resp.Body.Close()

			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			if tt.wantBody != "" {
				assert.Equal(t, tt.wantBody, string(body))
				assert.Equal(t, "bytes", resp.Header.Get("Accept-Ranges"))
			}
			if tt.wantContentType != "" {
				assert.Equal(t, tt.wantContentType, resp.Header.Get("Content-Type"))
			}
			if tt.wantRange != "" {
				assert.Equal(t, tt.wantRange, resp.Header.Get("Content-Range"))
			}
			client.AssertExpectations(t)
		})
	}
}

func Test_newS3Options(t *testing.T) {
	t.Run("aws endpoint resolved from region", func(t *testing.T) {
		opts := newS3Options(models.StorageConfiguration{
			Region:    "us-east-1",
			AccessKey: "key",
			SecretKey: "secret",
		})

		assert.Equal(t, "us-east-1", opts.Region)
		assert.Nil(t, opts.BaseEndpoint)
		assert.False(t, opts.UsePathStyle)
	})

	t.Run("custom endpoint uses path style", func(t *testing.T) {
		opts := newS3Options(models.StorageConfiguration{
			Region:    "auto",
			Endpoint:  "http://minio:9000",
			AccessKey: "key",
			SecretKey: "secret",
		})

		assert.Equal(t, "auto", opts.Region)
		require.NotNil(t, opts.BaseEndpoint)
		assert.Equal(t, "http://minio:9000", *opts.BaseEndpoint)
		assert.True(t, opts.UsePathStyle)
	})
}
