package favicon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/markhc/isrv/internal/logging"
)

func FetchFavicon(ctx context.Context, url string) ([]byte, error) {
	// We enforce a max size of 4KiB for the favicon to prevent problems
	// This is mostly due to the fact that the favicon is fetched and stored in memory
	const maxFaviconSize = int64(4 * 1024) // 4 KiB

	if url == "" {
		return nil, nil
	}

	isHttpURL := strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
	isLocalFile := !isHttpURL || strings.HasPrefix(url, "file://")

	if isLocalFile {
		localPath := strings.TrimPrefix(url, "file://")

		return loadFaviconFromFile(localPath, maxFaviconSize)
	} else if isHttpURL {
		return loadFaviconFromUrl(ctx, url, maxFaviconSize)
	}

	return nil, fmt.Errorf("unsupported favicon URL: %s", url)
}

func loadFaviconFromUrl(ctx context.Context, url string, maxFaviconSize int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for favicon URL: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch favicon from URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch favicon: received status code %d", resp.StatusCode)
	}

	if resp.ContentLength > maxFaviconSize {
		return nil, errors.New("favicon size exceeds the maximum allowed limit of 4KiB")
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read favicon data: %w", err)
	}

	logging.LogInfo("favicon fetched and saved successfully", logging.String("url", url))

	return data, nil
}

func loadFaviconFromFile(localPath string, maxFaviconSize int64) ([]byte, error) {
	fileInfo, err := os.Stat(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to access favicon file: %w", err)
	}

	if fileInfo.Size() > maxFaviconSize {
		return nil, errors.New("favicon file size exceeds the maximum allowed limit of 4KiB")
	}

	data, err := os.ReadFile(localPath) // #nosec G304
	if err != nil {
		return nil, fmt.Errorf("failed to read favicon from local file: %w", err)
	}

	logging.LogInfo("favicon loaded from local file successfully", logging.String("path", localPath))

	return data, nil
}
