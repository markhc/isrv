package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
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

func Test_S3Storage_Save(t *testing.T) {
	tests := []struct {
		name    string
		opts    SaveOptions
		putErr  error
		wantErr bool
	}{
		{
			name: "success",
			opts: SaveOptions{Size: 5, ContentType: "text/plain", Filename: "test.txt"},
		},
		{
			name: "unknown length reader",
			opts: SaveOptions{Size: -1, ContentType: "application/octet-stream"},
		},
		{
			name:    "put error",
			opts:    SaveOptions{Size: 5, ContentType: "text/plain", Filename: "test.txt"},
			putErr:  errors.New("write failed"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantContentType := tt.opts.ContentType
			wantSize := tt.opts.Size

			uploader := &MockS3Uploader{}
			uploader.On("UploadObject", mock.MatchedBy(func(p *transfermanager.UploadObjectInput) bool {
				lengthOK := p.ContentLength == nil && wantSize < 0 ||
					p.ContentLength != nil && *p.ContentLength == wantSize

				return p.Key != nil && *p.Key == "files/test-id" &&
					p.ContentType != nil && *p.ContentType == wantContentType &&
					lengthOK
			})).Return((*transfermanager.UploadObjectOutput)(nil), tt.putErr)

			s := newTestS3Storage(nil, nil, uploader)
			gotID, err := s.Save(context.Background(), "test-id", bytes.NewReader([]byte("hello")), tt.opts)

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

func Test_S3Storage_PresignedURL(t *testing.T) {
	tests := []struct {
		name       string
		proxy      bool
		file       models.File
		presignErr error
		presignURL string
		wantURL    string
		wantOK     bool
		wantErr    bool
	}{
		{
			name:       "presigned url returned",
			file:       models.File{ID: "test-id", Name: "file.bin"},
			presignURL: "https://s3.example.com/presigned",
			wantURL:    "https://s3.example.com/presigned",
			wantOK:     true,
		},
		{
			name:       "custom content type",
			file:       models.File{ID: "test-id", Name: "image.png", ContentType: "image/png"},
			presignURL: "https://s3.example.com/presigned-img",
			wantURL:    "https://s3.example.com/presigned-img",
			wantOK:     true,
		},
		{
			name:       "presign error",
			file:       models.File{ID: "test-id", Name: "file.bin"},
			presignErr: errors.New("sign failed"),
			wantErr:    true,
		},
		{
			name:  "proxy mode does not presign",
			proxy: true,
			file:  models.File{ID: "test-id", Name: "file.bin"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			presigner := &MockS3Presigner{}
			if !tt.proxy {
				var returnReq *v4.PresignedHTTPRequest
				if tt.presignErr == nil {
					returnReq = &v4.PresignedHTTPRequest{URL: tt.presignURL, Method: http.MethodGet}
				}
				presigner.On("PresignGetObject", mock.MatchedBy(func(p *s3.GetObjectInput) bool {
					return p.Key != nil && *p.Key == "files/test-id"
				})).Return(returnReq, tt.presignErr)
			}

			s := newTestS3Storage(nil, presigner, nil)
			s.ProxyDownloads = tt.proxy

			gotURL, gotOK, err := s.PresignedURL(context.Background(), &tt.file)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantURL, gotURL)
				assert.Equal(t, tt.wantOK, gotOK)
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

func Test_S3Storage_Open(t *testing.T) {
	content := []byte("hello proxy world")

	tests := []struct {
		name         string
		brange       *ByteRange
		output       *s3.GetObjectOutput
		getErr       error
		wantRange    string
		wantErr      bool
		wantSentinel error
		wantBody     string
		wantCT       string
		wantLength   int64
		wantPartial  bool
		wantCRange   string
	}{
		{
			name: "full download",
			output: &s3.GetObjectOutput{
				Body:          io.NopCloser(bytes.NewReader(content)),
				ContentLength: aws.Int64(int64(len(content))),
				ContentType:   aws.String("text/plain"),
			},
			wantBody:   string(content),
			wantCT:     "text/plain",
			wantLength: int64(len(content)),
		},
		{
			name: "content type from object metadata",
			output: &s3.GetObjectOutput{
				Body:          io.NopCloser(bytes.NewReader(content)),
				ContentLength: aws.Int64(int64(len(content))),
				ContentType:   aws.String("application/pdf"),
			},
			wantBody:   string(content),
			wantCT:     "application/pdf",
			wantLength: int64(len(content)),
		},
		{
			name:   "range request returns partial content",
			brange: &ByteRange{Start: 0, End: 4},
			output: &s3.GetObjectOutput{
				Body:          io.NopCloser(bytes.NewReader(content[:5])),
				ContentLength: aws.Int64(5),
				ContentRange:  aws.String("bytes 0-4/17"),
			},
			wantRange:   "bytes=0-4",
			wantBody:    string(content[:5]),
			wantLength:  5,
			wantPartial: true,
			wantCRange:  "bytes 0-4/17",
		},
		{
			name:         "missing object maps to ErrObjectNotFound",
			getErr:       &types.NoSuchKey{},
			wantErr:      true,
			wantSentinel: ErrObjectNotFound,
		},
		{
			name:         "invalid range maps to ErrInvalidRange",
			brange:       &ByteRange{Start: 999, End: -1},
			getErr:       &invalidRangeError{},
			wantRange:    "bytes=999-",
			wantErr:      true,
			wantSentinel: ErrInvalidRange,
		},
		{
			name:    "other error returns error",
			getErr:  errors.New("connection refused"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &MockS3Client{}
			client.On("GetObject", mock.MatchedBy(func(p *s3.GetObjectInput) bool {
				if p.Key == nil || *p.Key != "files/test-id" {
					return false
				}
				if tt.wantRange == "" {
					return p.Range == nil
				}
				return p.Range != nil && *p.Range == tt.wantRange
			})).Return(tt.output, tt.getErr)

			s := newTestS3Storage(client, nil, nil)

			obj, err := s.Open(context.Background(), "test-id", tt.brange)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantSentinel != nil {
					assert.ErrorIs(t, err, tt.wantSentinel)
				}
				client.AssertExpectations(t)
				return
			}

			require.NoError(t, err)
			body, readErr := io.ReadAll(obj.Body)
			require.NoError(t, readErr)
			require.NoError(t, obj.Body.Close())

			assert.Equal(t, tt.wantBody, string(body))
			assert.Equal(t, tt.wantLength, obj.Length)
			assert.Equal(t, tt.wantPartial, obj.Partial)
			assert.Equal(t, tt.wantCT, obj.ContentType)
			if tt.wantCRange != "" {
				assert.Equal(t, tt.wantCRange, obj.ContentRange)
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
