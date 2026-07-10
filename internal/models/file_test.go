package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_File_IsEncrypted(t *testing.T) {
	tests := []struct {
		name    string
		version int
		want    bool
	}{
		{"plaintext (zero)", 0, false},
		{"encrypted v1", 1, true},
		{"encrypted higher version", 5, true},
		{"negative treated as not encrypted", -1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &File{EncryptionVersion: tt.version}
			assert.Equal(t, tt.want, f.IsEncrypted())
		})
	}
}
