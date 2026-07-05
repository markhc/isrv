// Package encryption provides server-side encryption at rest for stored
// objects, built on the age file format (age-encryption.org/v1). Each stored
// object carries its own per-file key wrapped to the server's identity inside
// the age header, so no key material is persisted in the database.
package encryption

import (
	"errors"
	"fmt"
	"io"
	"os"

	"filippo.io/age"
	"github.com/markhc/isrv/internal/models"
)

// Scheme versions persisted in the files table.
const (
	// VersionNone marks an object stored in plaintext.
	VersionNone = 0
	// VersionAgeV1 marks an object stored using age-encryption.org/v1 with an
	// X25519 recipient.
	VersionAgeV1 = 1
)

// ErrWrongKey reports that the configured identity cannot decrypt the file
// (wrong master key, or corrupted header).
var ErrWrongKey = errors.New("encryption: configured identity cannot decrypt file")

// Manager wraps the server's age identity. A nil *Manager means
// encryption/decryption is unavailable.
type Manager struct {
	// identities holds every configured identity. The first is used to derive
	// the encryption recipient; all are tried on decrypt, which gives
	// master-key rotation for reads.
	identities []age.Identity
	recipient  *age.X25519Recipient
}

// NewManager builds a Manager from configuration. It returns (nil, nil) when
// no identity is configured. It returns an error when both identity sources
// are set, or when the configured identity cannot be parsed or read.
func NewManager(cfg models.EncryptionConfiguration) (*Manager, error) {
	if cfg.Identity != "" && cfg.IdentityFile != "" {
		return nil, errors.New("encryption: set either identity or identityFile, not both")
	}

	switch {
	case cfg.Identity != "":
		id, err := age.ParseX25519Identity(cfg.Identity)
		if err != nil {
			return nil, fmt.Errorf("encryption: parse identity: %w", err)
		}

		return &Manager{identities: []age.Identity{id}, recipient: id.Recipient()}, nil

	case cfg.IdentityFile != "":
		return newManagerFromFile(cfg.IdentityFile)

	default:
		// No identity configured: encryption/decryption is unavailable. A nil
		// *Manager is a valid, documented state, so nil error is correct here.
		//nolint:nilnil
		return nil, nil
	}
}

// newManagerFromFile builds a Manager from an age-keygen output file. The
// first X25519 identity is used for encryption; all parsed identities are
// retained for decryption.
func newManagerFromFile(path string) (*Manager, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("encryption: open identity file: %w", err)
	}
	defer f.Close()

	identities, err := age.ParseIdentities(f)
	if err != nil {
		return nil, fmt.Errorf("encryption: parse identity file: %w", err)
	}

	if len(identities) == 0 {
		return nil, fmt.Errorf("encryption: no identities found in %q", path)
	}

	recipient, err := firstX25519Recipient(identities)
	if err != nil {
		return nil, err
	}

	return &Manager{identities: identities, recipient: recipient}, nil
}

// firstX25519Recipient returns the recipient of the first X25519 identity in
// the list, which is used to encrypt new uploads.
func firstX25519Recipient(identities []age.Identity) (*age.X25519Recipient, error) {
	for _, id := range identities {
		if x, ok := id.(*age.X25519Identity); ok {
			return x.Recipient(), nil
		}
	}

	return nil, errors.New("encryption: identity file contains no X25519 identity")
}

// Encrypt wraps r so reads produce the age ciphertext stream.
func (m *Manager) Encrypt(r io.Reader) (io.Reader, error) {
	enc, err := age.EncryptReader(r, m.recipient)
	if err != nil {
		return nil, fmt.Errorf("encryption: build encrypt reader: %w", err)
	}

	return enc, nil
}

// Decrypt authenticates the header and wraps r so reads produce plaintext. It
// returns ErrWrongKey (wrapping the age error) when no configured identity
// matches. Tampered payload chunks surface as read errors mid-stream.
func (m *Manager) Decrypt(r io.Reader) (io.Reader, error) {
	dec, err := age.Decrypt(r, m.identities...)
	if err != nil {
		var noMatch *age.NoIdentityMatchError
		if errors.As(err, &noMatch) {
			return nil, fmt.Errorf("%w: %w", ErrWrongKey, err)
		}

		return nil, fmt.Errorf("encryption: decrypt: %w", err)
	}

	return dec, nil
}

// GenerateIdentity returns a new age X25519 identity string.
func GenerateIdentity() (string, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", fmt.Errorf("encryption: generate identity: %w", err)
	}

	return id.String(), nil
}
