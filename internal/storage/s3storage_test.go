package storage

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---- mock types ----

type MockS3Client struct{ mock.Mock }

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

// ---- test helpers ----

func newTestS3Storage(client s3api, presigner s3presigner) *S3Storage {
	return &S3Storage{
		Endpoint:  "http://test",
		Bucket:    "test-bucket",
		Region:    "us-east-1",
		BasePath:  "files",
		client:    client,
		presigner: presigner,
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

			s := newTestS3Storage(client, nil)
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
			client := &MockS3Client{}
			client.On("PutObject", mock.MatchedBy(func(p *s3.PutObjectInput) bool {
				return p.Key != nil && *p.Key == "files/test-id"
			})).Return((*s3.PutObjectOutput)(nil), tt.putErr)

			s := newTestS3Storage(client, nil)
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
			client.AssertExpectations(t)
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

			s := newTestS3Storage(client, nil)
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
		fileName       string
		metadata       map[string]string
		inlineContent  bool
		cachingEnabled bool
		presignErr     error
		presignURL     string
		wantStatus     int
		wantLocation   string
	}{
		{
			name:         "attachment no cache",
			fileName:     "file.bin",
			metadata:     map[string]string{},
			presignURL:   "https://s3.example.com/presigned",
			wantStatus:   http.StatusFound,
			wantLocation: "https://s3.example.com/presigned",
		},
		{
			name:           "inline with cache and custom content type",
			fileName:       "image.png",
			metadata:       map[string]string{"Content-Type": "image/png"},
			inlineContent:  true,
			cachingEnabled: true,
			presignURL:     "https://s3.example.com/presigned-img",
			wantStatus:     http.StatusFound,
			wantLocation:   "https://s3.example.com/presigned-img",
		},
		{
			name:       "presign error returns 500",
			fileName:   "file.bin",
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

			s := newTestS3Storage(nil, presigner)

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/", nil)

			s.ServeFile(w, r, "test-id", tt.fileName, tt.metadata, tt.inlineContent, tt.cachingEnabled)

			assert.Equal(t, tt.wantStatus, w.Code)
			if tt.wantLocation != "" {
				assert.Equal(t, tt.wantLocation, w.Header().Get("Location"))
			}
			presigner.AssertExpectations(t)
		})
	}
}
