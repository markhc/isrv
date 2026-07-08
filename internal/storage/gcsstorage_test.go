package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	gcs "cloud.google.com/go/storage"
	"github.com/markhc/isrv/internal/models"
	"github.com/markhc/isrv/internal/storage"
	"github.com/markhc/isrv/internal/storage/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
)

func newTestGCSStorage(bucket storage.GCSBucket) *storage.GCSStorage {
	return &storage.GCSStorage{
		Bucket:   "test-bucket",
		BasePath: "files",
		Client:   bucket,
	}
}

func Test_GCSStorage_FileExists(t *testing.T) {
	tests := []struct {
		name       string
		statErr    error
		wantExists bool
		wantErr    bool
	}{
		{name: "object exists", wantExists: true},
		{name: "object not found", statErr: gcs.ErrObjectNotExist},
		{name: "other error", statErr: errors.New("connection refused"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket := mocks.NewMockGCSBucket(t)
			bucket.EXPECT().StatObject(mock.Anything, "files/test-id").Return(tt.statErr)

			s := newTestGCSStorage(bucket)
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

func Test_GCSStorage_Save(t *testing.T) {
	tests := []struct {
		name      string
		opts      storage.SaveOptions
		chunkSize int
		uploadErr error
		wantMeta  storage.GCSObjectMeta
		wantErr   bool
	}{
		{
			name: "success with metadata",
			opts: storage.SaveOptions{Size: 5, ContentType: "text/plain", Filename: "test.txt"},
			wantMeta: storage.GCSObjectMeta{
				ContentType:        "text/plain",
				ContentDisposition: `inline; filename="test.txt"`,
			},
		},
		{
			name:     "unknown length reader",
			opts:     storage.SaveOptions{Size: -1, ContentType: "application/octet-stream"},
			wantMeta: storage.GCSObjectMeta{ContentType: "application/octet-stream"},
		},
		{
			name:      "custom chunk size",
			opts:      storage.SaveOptions{Size: 5},
			chunkSize: 8 * 1024 * 1024,
			wantMeta:  storage.GCSObjectMeta{ChunkSizeBytes: 8 * 1024 * 1024},
		},
		{
			name:      "upload error",
			opts:      storage.SaveOptions{Size: 5, ContentType: "text/plain"},
			uploadErr: errors.New("write failed"),
			wantMeta:  storage.GCSObjectMeta{ContentType: "text/plain"},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var uploaded bytes.Buffer

			bucket := mocks.NewMockGCSBucket(t)
			bucket.EXPECT().Upload(mock.Anything, "files/test-id", tt.wantMeta, mock.Anything).
				RunAndReturn(func(_ context.Context, _ string, _ storage.GCSObjectMeta, r io.Reader) error {
					if tt.uploadErr != nil {
						return tt.uploadErr
					}

					_, err := io.Copy(&uploaded, r)

					return err
				})

			s := newTestGCSStorage(bucket)
			s.ChunkSizeBytes = tt.chunkSize

			gotID, err := s.Save(context.Background(), "test-id", bytes.NewReader([]byte("hello")), tt.opts)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "test-id", gotID)
				assert.Equal(t, "hello", uploaded.String())
			}
		})
	}
}

func Test_GCSStorage_DeleteFile(t *testing.T) {
	tests := []struct {
		name      string
		deleteErr error
		wantErr   bool
	}{
		{name: "success"},
		{name: "missing object is already deleted", deleteErr: gcs.ErrObjectNotExist},
		{name: "delete error", deleteErr: errors.New("forbidden"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket := mocks.NewMockGCSBucket(t)
			bucket.EXPECT().DeleteObject(mock.Anything, "files/test-id").Return(tt.deleteErr)

			s := newTestGCSStorage(bucket)
			err := s.DeleteFile(context.Background(), "test-id")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func Test_GCSStorage_Open(t *testing.T) {
	content := []byte("hello proxy world")

	tests := []struct {
		name         string
		brange       *storage.ByteRange
		result       *storage.GCSReadResult
		readErr      error
		wantOffset   int64
		wantLength   int64
		wantErr      bool
		wantSentinel error
		wantBody     string
		wantCT       string
		wantObjLen   int64
		wantSize     int64
		wantPartial  bool
		wantCRange   string
	}{
		{
			name: "full download",
			result: &storage.GCSReadResult{
				Body:        io.NopCloser(bytes.NewReader(content)),
				Size:        int64(len(content)),
				Remain:      int64(len(content)),
				ContentType: "text/plain",
			},
			wantOffset: 0,
			wantLength: -1,
			wantBody:   string(content),
			wantCT:     "text/plain",
			wantObjLen: int64(len(content)),
			wantSize:   int64(len(content)),
		},
		{
			name:   "bounded range returns partial content",
			brange: &storage.ByteRange{Start: 0, End: 4},
			result: &storage.GCSReadResult{
				Body:   io.NopCloser(bytes.NewReader(content[:5])),
				Size:   int64(len(content)),
				Remain: 5,
			},
			wantOffset:  0,
			wantLength:  5,
			wantBody:    string(content[:5]),
			wantObjLen:  5,
			wantSize:    int64(len(content)),
			wantPartial: true,
			wantCRange:  "bytes 0-4/17",
		},
		{
			name:   "open-ended range",
			brange: &storage.ByteRange{Start: 12, End: -1},
			result: &storage.GCSReadResult{
				Body:        io.NopCloser(bytes.NewReader(content[12:])),
				Size:        int64(len(content)),
				Remain:      5,
				StartOffset: 12,
			},
			wantOffset:  12,
			wantLength:  -1,
			wantBody:    string(content[12:]),
			wantObjLen:  5,
			wantSize:    int64(len(content)),
			wantPartial: true,
			wantCRange:  "bytes 12-16/17",
		},
		{
			name:   "suffix range reads last bytes",
			brange: &storage.ByteRange{Suffix: 5},
			result: &storage.GCSReadResult{
				Body:        io.NopCloser(bytes.NewReader(content[12:])),
				Size:        int64(len(content)),
				Remain:      5,
				StartOffset: 12,
			},
			wantOffset:  -5,
			wantLength:  -1,
			wantBody:    string(content[12:]),
			wantObjLen:  5,
			wantSize:    int64(len(content)),
			wantPartial: true,
			wantCRange:  "bytes 12-16/17",
		},
		{
			name:         "missing object",
			readErr:      gcs.ErrObjectNotExist,
			wantOffset:   0,
			wantLength:   -1,
			wantErr:      true,
			wantSentinel: storage.ErrObjectNotFound,
		},
		{
			name:         "unsatisfiable range",
			brange:       &storage.ByteRange{Start: 100, End: -1},
			readErr:      &googleapi.Error{Code: http.StatusRequestedRangeNotSatisfiable},
			wantOffset:   100,
			wantLength:   -1,
			wantErr:      true,
			wantSentinel: storage.ErrInvalidRange,
		},
		{
			name:       "other error",
			readErr:    errors.New("connection reset"),
			wantOffset: 0,
			wantLength: -1,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket := mocks.NewMockGCSBucket(t)
			bucket.EXPECT().NewRangeReader(mock.Anything, "files/test-id", tt.wantOffset, tt.wantLength).
				Return(tt.result, tt.readErr)

			s := newTestGCSStorage(bucket)
			got, err := s.Open(context.Background(), "test-id", tt.brange)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantSentinel != nil {
					require.ErrorIs(t, err, tt.wantSentinel)
				}
			} else {
				require.NoError(t, err)
				body, readErr := io.ReadAll(got.Body)
				require.NoError(t, readErr)
				require.NoError(t, got.Body.Close())

				assert.Equal(t, tt.wantBody, string(body))
				assert.Equal(t, tt.wantCT, got.ContentType)
				assert.Equal(t, tt.wantObjLen, got.Length)
				assert.Equal(t, tt.wantSize, got.Size)
				assert.Equal(t, tt.wantPartial, got.Partial)
				assert.Equal(t, tt.wantCRange, got.ContentRange)
			}
		})
	}
}

func Test_GCSStorage_PresignedURL(t *testing.T) {
	tests := []struct {
		name    string
		proxy   bool
		file    models.File
		signErr error
		signURL string
		wantCT  string
		wantURL string
		wantOK  bool
		wantErr bool
	}{
		{
			name:    "signed url returned",
			file:    models.File{ID: "test-id", Name: "file.bin"},
			signURL: "https://storage.googleapis.com/signed",
			wantCT:  "application/octet-stream",
			wantURL: "https://storage.googleapis.com/signed",
			wantOK:  true,
		},
		{
			name:    "custom content type",
			file:    models.File{ID: "test-id", Name: "image.png", ContentType: "image/png"},
			signURL: "https://storage.googleapis.com/signed-img",
			wantCT:  "image/png",
			wantURL: "https://storage.googleapis.com/signed-img",
			wantOK:  true,
		},
		{
			name:    "sign error",
			file:    models.File{ID: "test-id", Name: "file.bin"},
			signErr: errors.New("sign failed"),
			wantCT:  "application/octet-stream",
			wantErr: true,
		},
		{
			name:  "proxy mode does not sign",
			proxy: true,
			file:  models.File{ID: "test-id", Name: "file.bin"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket := mocks.NewMockGCSBucket(t)
			if !tt.proxy {
				wantCT := tt.wantCT
				bucket.EXPECT().SignedURL("files/test-id", mock.MatchedBy(func(opts *gcs.SignedURLOptions) bool {
					return opts.Scheme == gcs.SigningSchemeV4 &&
						opts.Method == http.MethodGet &&
						opts.QueryParameters.Get("response-content-type") == wantCT &&
						opts.QueryParameters.Get("response-content-disposition") != ""
				})).Return(tt.signURL, tt.signErr)
			}

			s := newTestGCSStorage(bucket)
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

func Test_GCSStorage_HealthCheck(t *testing.T) {
	tests := []struct {
		name    string
		pingErr error
		wantErr bool
	}{
		{name: "bucket reachable"},
		{name: "bucket unreachable", pingErr: errors.New("permission denied"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket := mocks.NewMockGCSBucket(t)
			bucket.EXPECT().Ping(mock.Anything, "files").Return(tt.pingErr)

			s := newTestGCSStorage(bucket)
			err := s.HealthCheck(context.Background())

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
