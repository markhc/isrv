package storage

import (
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path"

	"github.com/markhc/isrv/internal/headers"
	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
)

// LocalStorage implements the Storage interface for local filesystem storage
type LocalStorage struct {
	BasePath string
}

func NewLocalStorage(config models.StorageConfiguration) *LocalStorage {
	return &LocalStorage{BasePath: config.BasePath}
}

func (ls *LocalStorage) FileExists(fileID string) (bool, error) {
	filePath := path.Join(ls.BasePath, fileID)
	_, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func (ls *LocalStorage) SaveFile(fileID string, data []byte) (string, error) {
	if exists, _ := ls.FileExists(fileID); exists {
		return "", fs.ErrExist
	}

	filePath := path.Join(ls.BasePath, fileID)
	err := os.WriteFile(filePath, data, 0644)
	if err != nil {
		return "", err
	}
	return filePath, nil
}
func (ls *LocalStorage) SaveFileUpload(fileID string, file multipart.File) (string, error) {
	filePath := path.Join(ls.BasePath, fileID)

	dst, err := os.Create(filePath)
	if err != nil {
		logging.LogError("Failed to create file", logging.Error(err))
		return "", err
	}
	defer dst.Close()

	_, err = io.Copy(dst, file)
	if err != nil {
		logging.LogError("Failed to save file", logging.Error(err))
		return "", err
	}

	return filePath, nil
}
func (ls *LocalStorage) RetrieveFile(fileID string) ([]byte, error) {
	filePath := path.Join(ls.BasePath, fileID)
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (ls *LocalStorage) DeleteFile(fileID string) error {
	filePath := path.Join(ls.BasePath, fileID)
	err := os.Remove(filePath)
	if err != nil {
		return err
	}
	return nil
}

func (ls *LocalStorage) ServeFile(w http.ResponseWriter, r *http.Request, fileID string, fileName string, inlineContent bool, cachingEnabled bool) {
	headers.SetHeaders(w, fileName, inlineContent, cachingEnabled)
	http.ServeFile(w, r, path.Join(ls.BasePath, fileID))
}
