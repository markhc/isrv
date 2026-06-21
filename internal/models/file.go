package models

import "time"

type File struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Size        int64     `json:"size"`
	ContentType string    `json:"contentType"`
	Expiration  time.Time `json:"expiration"`
	Downloads   int       `json:"downloads"`
}
