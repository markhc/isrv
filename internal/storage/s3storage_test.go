package storage_test

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
	"github.com/markhc/isrv/internal/storage"
	"github.com/markhc/isrv/internal/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestS3Storage(
	client storage.S3Client,
	presigner storage.S3Presigner,
	uploader storage.S3Uploader,
) *storage.S3Storage {
	return &storage.S3Storage{
		Bucket:    "test-bucket",
		BasePath:  "files",
		Client:    client,
		Presigner: presigner,
		Uploader:  uploader,
	}
}

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
			client := mocks.NewMockS3Client(t)
			client.EXPECT().HeadObject(mock.Anything, mock.MatchedBy(func(p *s3.HeadObjectInput) bool {
				return p.Key != nil && *p.Key == "files/test-id"
			})).Return(nil, tt.headErr)

			s := newTestS3Storage(client, nil, nil)
			got, err := s.FileExists(context.Background(), "test-id")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantExists, got)
			}
		})
	}
}

func Test_S3Storage_Save(t *testing.T) {
	tests := []struct {
		name    string
		opts    storage.SaveOptions
		putErr  error
		wantErr bool
	}{
		{
			name: "success",
			opts: storage.SaveOptions{Size: 5, ContentType: "text/plain", Filename: "test.txt"},
		},
		{
			name: "unknown length reader",
			opts: storage.SaveOptions{Size: -1, ContentType: "application/octet-stream"},
		},
		{
			name:    "put error",
			opts:    storage.SaveOptions{Size: 5, ContentType: "text/plain", Filename: "test.txt"},
			putErr:  errors.New("write failed"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantContentType := tt.opts.ContentType
			wantSize := tt.opts.Size

			uploader := mocks.NewMockS3Uploader(t)
			uploader.EXPECT().UploadObject(mock.Anything, mock.MatchedBy(func(p *transfermanager.UploadObjectInput) bool {
				lengthOK := p.ContentLength == nil && wantSize < 0 ||
					p.ContentLength != nil && *p.ContentLength == wantSize

				return p.Key != nil && *p.Key == "files/test-id" &&
					p.ContentType != nil && *p.ContentType == wantContentType &&
					lengthOK
			})).Return(nil, tt.putErr)

			s := newTestS3Storage(nil, nil, uploader)
			gotID, err := s.Save(context.Background(), "test-id", bytes.NewReader([]byte("hello")), tt.opts)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "test-id", gotID)
			}
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
			client := mocks.NewMockS3Client(t)
			client.EXPECT().DeleteObject(mock.Anything, mock.MatchedBy(func(p *s3.DeleteObjectInput) bool {
				return p.Key != nil && *p.Key == "files/test-id"
			})).Return(nil, tt.deleteErr)

			s := newTestS3Storage(client, nil, nil)
			err := s.DeleteFile(context.Background(), "test-id")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
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
			presigner := mocks.NewMockS3Presigner(t)
			if !tt.proxy {
				var returnReq *v4.PresignedHTTPRequest
				if tt.presignErr == nil {
					returnReq = &v4.PresignedHTTPRequest{URL: tt.presignURL, Method: http.MethodGet}
				}
				// The third matcher covers the s3.WithPresignExpires variadic option.
				presigner.EXPECT().PresignGetObject(mock.Anything, mock.MatchedBy(func(p *s3.GetObjectInput) bool {
					return p.Key != nil && *p.Key == "files/test-id"
				}), mock.Anything).Return(returnReq, tt.presignErr)
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
		brange       *storage.ByteRange
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
			brange: &storage.ByteRange{Start: 0, End: 4},
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
			wantSentinel: storage.ErrObjectNotFound,
		},
		{
			name:         "invalid range maps to ErrInvalidRange",
			brange:       &storage.ByteRange{Start: 999, End: -1},
			getErr:       &invalidRangeError{},
			wantRange:    "bytes=999-",
			wantErr:      true,
			wantSentinel: storage.ErrInvalidRange,
		},
		{
			name:    "other error returns error",
			getErr:  errors.New("connection refused"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := mocks.NewMockS3Client(t)
			client.EXPECT().GetObject(mock.Anything, mock.MatchedBy(func(p *s3.GetObjectInput) bool {
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
		})
	}
}
