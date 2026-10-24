package clientencryption

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	VersionV1       = 1
	GenericFilename = "encrypted-file.isrv"
	ContentType     = "application/octet-stream"

	chunkSize               = 1024 * 1024
	headerSize              = 8 + 1 + 1 + 4 + 16 + 8
	metadataCiphertextSize  = 4096 + 16
	authenticationTagSize   = 16
	minimumV1CiphertextSize = headerSize + metadataCiphertextSize
	magic                   = "ISRV-E2E"
)

// MaxCiphertextSize returns the largest valid v1 container for a plaintext
// size limit. Metadata, header, and per-record authentication overhead are
// included because the server cannot read the encrypted plaintext size.
func MaxCiphertextSize(maxPlaintextSize int64) int64 {
	records := (maxPlaintextSize + chunkSize - 1) / chunkSize
	return maxPlaintextSize + minimumV1CiphertextSize + records*authenticationTagSize
}

// ValidateV1Header validates the cleartext portion of a declared v1 container
// and restores reader to the beginning. It cannot authenticate encrypted data.
func ValidateV1Header(reader io.ReadSeeker, size int64) error {
	if size < minimumV1CiphertextSize {
		return errors.New("client-encrypted file is too short")
	}

	header := make([]byte, headerSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return fmt.Errorf("read client-encryption header: %w", err)
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind client-encrypted file: %w", err)
	}

	if string(header[:len(magic)]) != magic {
		return errors.New("client-encryption magic is invalid")
	}
	if header[len(magic)] != VersionV1 {
		return fmt.Errorf("unsupported client-encryption container version: %d", header[len(magic)])
	}
	if header[len(magic)+1] != 0 {
		return errors.New("client-encryption container uses unsupported flags")
	}
	if got := binary.BigEndian.Uint32(header[len(magic)+2 : len(magic)+6]); got != chunkSize {
		return fmt.Errorf("unsupported client-encryption chunk size: %d", got)
	}

	return nil
}
