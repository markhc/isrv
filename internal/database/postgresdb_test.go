package database

import (
	"testing"

	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
)

func Test_postgresDSN(t *testing.T) {
	tests := []struct {
		name     string
		config   models.DatabaseConfiguration
		expected string
	}{
		{
			name:     "explicit DSN takes precedence",
			config:   models.DatabaseConfiguration{DSN: "postgres://u:p@db:5432/files", Host: "ignored"},
			expected: "postgres://u:p@db:5432/files",
		},
		{
			name: "full parameters",
			config: models.DatabaseConfiguration{
				Host:     "localhost",
				Port:     5432,
				User:     "isrv",
				Password: "secret",
				DBName:   "files",
			},
			expected: "postgres://isrv:secret@localhost:5432/files",
		},
		{
			name: "user without password",
			config: models.DatabaseConfiguration{
				Host:   "localhost",
				Port:   5432,
				User:   "isrv",
				DBName: "files",
			},
			expected: "postgres://isrv@localhost:5432/files",
		},
		{
			name: "no port",
			config: models.DatabaseConfiguration{
				Host:   "localhost",
				User:   "isrv",
				DBName: "files",
			},
			expected: "postgres://isrv@localhost/files",
		},
		{
			name: "password with special characters is escaped",
			config: models.DatabaseConfiguration{
				Host:     "localhost",
				Port:     5432,
				User:     "isrv",
				Password: "p@ss/word",
				DBName:   "files",
			},
			expected: "postgres://isrv:p%40ss%2Fword@localhost:5432/files",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, postgresDSN(tt.config))
		})
	}
}
