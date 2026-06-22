package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIssueAndValidate_RoundTrip(t *testing.T) {
	secret := []byte("test-secret")

	token := IssueToken(secret, "admin", time.Hour)
	username, ok := Validate(secret, token)

	require.True(t, ok)
	assert.Equal(t, "admin", username)
}

func TestValidate_ExpiredToken(t *testing.T) {
	secret := []byte("test-secret")

	token := IssueToken(secret, "admin", -time.Minute)
	_, ok := Validate(secret, token)

	assert.False(t, ok)
}

func TestValidate_WrongSecret(t *testing.T) {
	token := IssueToken([]byte("secret-a"), "admin", time.Hour)
	_, ok := Validate([]byte("secret-b"), token)

	assert.False(t, ok)
}

func TestValidate_TamperedToken(t *testing.T) {
	secret := []byte("test-secret")
	token := IssueToken(secret, "admin", time.Hour)

	// Flip the last character of the signature.
	tampered := token[:len(token)-1]
	if token[len(token)-1] == 'A' {
		tampered += "B"
	} else {
		tampered += "A"
	}

	_, ok := Validate(secret, tampered)
	assert.False(t, ok)
}

func TestValidate_EmptySecret(t *testing.T) {
	_, ok := Validate(nil, "anything")
	assert.False(t, ok)
}

func TestValidate_MalformedToken(t *testing.T) {
	secret := []byte("test-secret")

	for _, token := range []string{"", "no-dots", "only.one"} {
		_, ok := Validate(secret, token)
		assert.False(t, ok, "token %q should be invalid", token)
	}
}
