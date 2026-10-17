package clientencryption

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validV1Container() []byte {
	container := make([]byte, minimumV1CiphertextSize)
	copy(container, []byte(magic))
	container[len(magic)] = VersionV1
	binary.BigEndian.PutUint32(container[len(magic)+2:], chunkSize)
	return container
}

func TestMaxCiphertextSize(t *testing.T) {
	assert.Equal(t, int64(minimumV1CiphertextSize), MaxCiphertextSize(0))
	assert.Equal(t, int64(minimumV1CiphertextSize+chunkSize+authenticationTagSize),
		MaxCiphertextSize(chunkSize))
}

func TestValidateV1Header(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func([]byte) []byte
		wantErr string
	}{
		{name: "valid"},
		{
			name: "too short",
			mutate: func(container []byte) []byte {
				return container[:minimumV1CiphertextSize-1]
			},
			wantErr: "too short",
		},
		{
			name: "invalid magic",
			mutate: func(container []byte) []byte {
				container[0] ^= 1
				return container
			},
			wantErr: "magic",
		},
		{
			name: "unsupported version",
			mutate: func(container []byte) []byte {
				container[len(magic)] = 2
				return container
			},
			wantErr: "version",
		},
		{
			name: "unsupported flags",
			mutate: func(container []byte) []byte {
				container[len(magic)+1] = 1
				return container
			},
			wantErr: "flags",
		},
		{
			name: "unsupported chunk size",
			mutate: func(container []byte) []byte {
				binary.BigEndian.PutUint32(container[len(magic)+2:], 4096)
				return container
			},
			wantErr: "chunk size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			container := validV1Container()
			if tt.mutate != nil {
				container = tt.mutate(container)
			}
			reader := bytes.NewReader(container)

			err := ValidateV1Header(reader, int64(len(container)))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			position, seekErr := reader.Seek(0, 1)
			require.NoError(t, seekErr)
			assert.Zero(t, position)
		})
	}
}
