package encryption

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestIdentity generates a fresh X25519 identity.
func newTestIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()

	id, err := age.GenerateX25519Identity()
	require.NoError(t, err)

	return id
}

// newTestManager builds a Manager from a freshly generated identity.
func newTestManager(t *testing.T) *Manager {
	t.Helper()

	m, err := NewManager(models.EncryptionConfiguration{Identity: newTestIdentity(t).String()})
	require.NoError(t, err)
	require.NotNil(t, m)

	return m
}

// encryptToBytes runs plaintext through m.Encrypt and returns the ciphertext.
func encryptToBytes(t *testing.T, m *Manager, plaintext []byte) []byte {
	t.Helper()

	r, err := m.Encrypt(bytes.NewReader(plaintext))
	require.NoError(t, err)

	ciphertext, err := io.ReadAll(r)
	require.NoError(t, err)

	return ciphertext
}

// writeIdentityFile writes the given lines as an identity file in a temp dir.
func writeIdentityFile(t *testing.T, lines ...string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "key.txt")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))

	return path
}

// testPattern returns size bytes of non-repeating data so that chunk ordering
// or offset bugs cannot cancel out.
func testPattern(size int) []byte {
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte(i % 251)
	}

	return buf
}

// stubIdentity is a non-X25519 age.Identity used to exercise recipient
// selection.
type stubIdentity struct{}

func (stubIdentity) Unwrap([]*age.Stanza) ([]byte, error) {
	return nil, age.ErrIncorrectIdentity
}

func Test_Manager_RoundTrip(t *testing.T) {
	const chunk = 64 * 1024 // age STREAM chunk size

	m := newTestManager(t)

	tests := []struct {
		name string
		size int
	}{
		{"empty", 0},
		{"single byte", 1},
		{"one below chunk boundary", chunk - 1},
		{"exact chunk boundary", chunk},
		{"one above chunk boundary", chunk + 1},
		{"multiple chunks", 5 * 1024 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plaintext := testPattern(tt.size)
			ciphertext := encryptToBytes(t, m, plaintext)

			if tt.size > 0 {
				assert.NotEqual(t, plaintext, ciphertext, "ciphertext must differ from plaintext")
			}
			assert.True(t, bytes.HasPrefix(ciphertext, []byte("age-encryption.org/v1")),
				"ciphertext must start with the age magic")

			decReader, err := m.Decrypt(bytes.NewReader(ciphertext))
			require.NoError(t, err)

			got, err := io.ReadAll(decReader)
			require.NoError(t, err)
			assert.Equal(t, plaintext, got)
		})
	}
}

func Test_Manager_Encrypt_freshFileKeyPerCall(t *testing.T) {
	m := newTestManager(t)

	plaintext := []byte("same plaintext")
	first := encryptToBytes(t, m, plaintext)
	second := encryptToBytes(t, m, plaintext)

	assert.NotEqual(t, first, second, "each encryption must use a fresh per-file key")
}

func Test_Manager_Decrypt_failures(t *testing.T) {
	writer := newTestManager(t)
	other := newTestManager(t) // different identity

	// Two full STREAM chunks so payload corruption sits past the header.
	ciphertext := encryptToBytes(t, writer, testPattern(128*1024))

	tests := []struct {
		name string
		// decryptor defaults to writer when nil.
		decryptor *Manager
		input     func() []byte
		// wantDecryptErr: the error surfaces from Decrypt itself (header
		// authentication). Otherwise Decrypt succeeds and the error must
		// surface mid-stream from the chunk MAC during the read.
		wantDecryptErr bool
		wantWrongKey   bool
	}{
		{
			name:           "wrong key",
			decryptor:      other,
			input:          func() []byte { return ciphertext },
			wantDecryptErr: true,
			wantWrongKey:   true,
		},
		{
			name: "tampered header",
			input: func() []byte {
				c := bytes.Clone(ciphertext)
				c[30] ^= 0xff
				return c
			},
			wantDecryptErr: true,
		},
		{
			name:           "not an age stream",
			input:          func() []byte { return []byte("clearly not ciphertext") },
			wantDecryptErr: true,
		},
		{
			name:           "truncated inside header",
			input:          func() []byte { return bytes.Clone(ciphertext)[:20] },
			wantDecryptErr: true,
		},
		{
			name: "tampered payload",
			input: func() []byte {
				c := bytes.Clone(ciphertext)
				c[len(c)-1] ^= 0xff
				return c
			},
		},
		{
			name:  "truncated payload",
			input: func() []byte { return bytes.Clone(ciphertext)[:len(ciphertext)-10] },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decryptor := tt.decryptor
			if decryptor == nil {
				decryptor = writer
			}

			decReader, err := decryptor.Decrypt(bytes.NewReader(tt.input()))
			if tt.wantDecryptErr {
				require.Error(t, err)

				if tt.wantWrongKey {
					assert.ErrorIs(t, err, ErrWrongKey)

					var noMatch *age.NoIdentityMatchError
					assert.True(t, errors.As(err, &noMatch), "original age error must remain unwrappable")
				} else {
					assert.NotErrorIs(t, err, ErrWrongKey,
						"parse and tamper failures must not be reported as a wrong key")
				}

				return
			}

			require.NoError(t, err, "payload corruption must not fail header authentication")

			_, err = io.ReadAll(decReader)
			require.Error(t, err, "payload corruption must surface as a read error")
		})
	}
}

func Test_Manager_KeyRotation(t *testing.T) {
	oldID := newTestIdentity(t)
	newID := newTestIdentity(t)

	// The rotated file lists the new identity first (used for new uploads) and
	// keeps the old one for reading existing objects.
	path := writeIdentityFile(t, newID.String(), oldID.String())

	m, err := NewManager(models.EncryptionConfiguration{IdentityFile: path})
	require.NoError(t, err)
	require.NotNil(t, m)

	t.Run("decrypts objects encrypted under the old identity", func(t *testing.T) {
		var buf bytes.Buffer
		w, err := age.Encrypt(&buf, oldID.Recipient())
		require.NoError(t, err)
		_, err = w.Write([]byte("legacy object"))
		require.NoError(t, err)
		require.NoError(t, w.Close())

		decReader, err := m.Decrypt(&buf)
		require.NoError(t, err)

		got, err := io.ReadAll(decReader)
		require.NoError(t, err)
		assert.Equal(t, []byte("legacy object"), got)
	})

	t.Run("encrypts new objects to the first identity only", func(t *testing.T) {
		ciphertext := encryptToBytes(t, m, []byte("new object"))

		onlyNew, err := NewManager(models.EncryptionConfiguration{Identity: newID.String()})
		require.NoError(t, err)
		_, err = onlyNew.Decrypt(bytes.NewReader(ciphertext))
		assert.NoError(t, err, "holder of the first identity must be able to decrypt")

		onlyOld, err := NewManager(models.EncryptionConfiguration{Identity: oldID.String()})
		require.NoError(t, err)
		_, err = onlyOld.Decrypt(bytes.NewReader(ciphertext))
		assert.ErrorIs(t, err, ErrWrongKey, "retired identity must not decrypt new objects")
	})
}

func Test_NewManager(t *testing.T) {
	id := newTestIdentity(t)

	tests := []struct {
		name    string
		cfg     func(t *testing.T) models.EncryptionConfiguration
		wantErr bool
		wantNil bool
	}{
		{
			name: "valid identity string",
			cfg: func(*testing.T) models.EncryptionConfiguration {
				return models.EncryptionConfiguration{Identity: id.String()}
			},
		},
		{
			name: "valid identity file with comments",
			cfg: func(t *testing.T) models.EncryptionConfiguration {
				path := writeIdentityFile(t,
					"# created: 2026-01-01",
					"# public key: "+id.Recipient().String(),
					id.String())
				return models.EncryptionConfiguration{IdentityFile: path}
			},
		},
		{
			name: "identity file with multiple identities",
			cfg: func(t *testing.T) models.EncryptionConfiguration {
				path := writeIdentityFile(t, id.String(), newTestIdentity(t).String())
				return models.EncryptionConfiguration{IdentityFile: path}
			},
		},
		{
			name: "garbage identity",
			cfg: func(*testing.T) models.EncryptionConfiguration {
				return models.EncryptionConfiguration{Identity: "not-a-key"}
			},
			wantErr: true,
			wantNil: true,
		},
		{
			name: "both sources set",
			cfg: func(*testing.T) models.EncryptionConfiguration {
				return models.EncryptionConfiguration{
					Identity:     id.String(),
					IdentityFile: "/tmp/whatever",
				}
			},
			wantErr: true,
			wantNil: true,
		},
		{
			name: "no source",
			cfg: func(*testing.T) models.EncryptionConfiguration {
				return models.EncryptionConfiguration{}
			},
			wantNil: true,
		},
		{
			name: "missing identity file",
			cfg: func(*testing.T) models.EncryptionConfiguration {
				return models.EncryptionConfiguration{IdentityFile: "/nonexistent/key.txt"}
			},
			wantErr: true,
			wantNil: true,
		},
		{
			name: "identity file with only comments",
			cfg: func(t *testing.T) models.EncryptionConfiguration {
				path := writeIdentityFile(t, "# created: 2026-01-01", "# no keys here")
				return models.EncryptionConfiguration{IdentityFile: path}
			},
			wantErr: true,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewManager(tt.cfg(t))

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.wantNil {
				assert.Nil(t, m)
			} else {
				assert.NotNil(t, m)
			}
		})
	}
}

func Test_firstX25519Recipient(t *testing.T) {
	id := newTestIdentity(t)

	tests := []struct {
		name       string
		identities []age.Identity
		wantErr    bool
	}{
		{
			name:       "single X25519 identity",
			identities: []age.Identity{id},
		},
		{
			name:       "skips non-X25519 identities",
			identities: []age.Identity{stubIdentity{}, id},
		},
		{
			name:       "no X25519 identity",
			identities: []age.Identity{stubIdentity{}},
			wantErr:    true,
		},
		{
			name:    "empty list",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recipient, err := firstX25519Recipient(tt.identities)

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, recipient)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, recipient)
			assert.Equal(t, id.Recipient().String(), recipient.String())
		})
	}
}

func Test_GenerateIdentity(t *testing.T) {
	s, err := GenerateIdentity()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(s, "AGE-SECRET-KEY-1"))

	// The generated identity must be parseable and usable.
	m, err := NewManager(models.EncryptionConfiguration{Identity: s})
	require.NoError(t, err)
	require.NotNil(t, m)
}
