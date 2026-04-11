package favicon

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/markhc/isrv/internal/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	logging.InitializeNop()
	os.Exit(m.Run())
}

func Test_fetchFavicon(t *testing.T) {
	t.Run("loads from local file without prefix", func(t *testing.T) {
		data := []byte{0x89, 0x50, 0x4E, 0x47}
		f, err := os.CreateTemp(t.TempDir(), "favicon*.png")
		require.NoError(t, err)
		f.Write(data)
		f.Close()

		result, err := FetchFavicon(context.Background(), f.Name())
		require.NoError(t, err)
		assert.Equal(t, data, result)
	})

	t.Run("loads from file:// prefixed path", func(t *testing.T) {
		data := []byte{0x00, 0x01, 0x02}
		f, err := os.CreateTemp(t.TempDir(), "favicon*.ico")
		require.NoError(t, err)
		f.Write(data)
		f.Close()

		result, err := FetchFavicon(context.Background(), "file://"+f.Name())
		require.NoError(t, err)
		assert.Equal(t, data, result)
	})

	t.Run("local file exceeding maxFaviconSize returns error", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "big*.png")
		require.NoError(t, err)
		f.Write(bytes.Repeat([]byte("x"), maxFaviconSize+1))
		f.Close()

		_, err = FetchFavicon(context.Background(), f.Name())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum allowed limit")
	})

	t.Run("http URL fetches and returns bytes", func(t *testing.T) {
		data := []byte{0x47, 0x49, 0x46} // GIF magic
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write(data)
		}))
		defer ts.Close()

		result, err := FetchFavicon(context.Background(), ts.URL+"/favicon.gif")
		require.NoError(t, err)
		assert.Equal(t, data, result)
	})

	t.Run("http URL non-200 response returns error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		_, err := FetchFavicon(context.Background(), ts.URL+"/missing.png")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "404")
	})

	t.Run("http URL exceeding maxFaviconSize Content-Length returns error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", maxFaviconSize+1))
			w.WriteHeader(http.StatusOK)
			w.Write(bytes.Repeat([]byte("x"), maxFaviconSize+1))
		}))
		defer ts.Close()

		_, err := FetchFavicon(context.Background(), ts.URL+"/big.png")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "maximum allowed limit")
	})
}
