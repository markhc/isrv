package models

import "time"

type File struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	ContentType string    `json:"contentType"`
	Expiration  time.Time `json:"expiration"`
	Downloads   int       `json:"downloads"`
	IPAddress   string    `json:"ipAddress,omitempty"`
	CreatedAt   time.Time `json:"createdAt,omitzero"`
}

// FileListFilter describes the query parameters for listing files in the admin
// panel. The zero value lists all files with default ordering.
type FileListFilter struct {
	// Search matches against the file name and content type (substring match).
	Search string
	// IP matches against the upload IP address (substring match).
	IP string
	// SortBy is the column to sort by: "created_at", "size", "downloads",
	// "expiration", or "name". Invalid values fall back to "created_at".
	SortBy string
	// SortDir is the sort direction: "asc" or "desc". Defaults to "desc".
	SortDir string
	// Limit is the maximum number of records to return.
	Limit int
	// Offset is the number of records to skip.
	Offset int
}
