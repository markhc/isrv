package handlers

import (
	"net/http"

	"github.com/markhc/isrv/internal/database"
	"github.com/markhc/isrv/internal/storage"
)

// Delete returns a handler that deletes a stored file by its ID.
// Not yet implemented.
func Delete(_ database.Database, _ storage.Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Implemented", http.StatusNotImplemented)
	}
}

// Expire returns a handler that updates the expiration of a stored file.
// Not yet implemented.
func Expire(_ database.Database) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Not Implemented", http.StatusNotImplemented)
	}
}
