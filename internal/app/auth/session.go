// Package auth provides session token handling for the admin panel.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// CookieName is the name of the admin session cookie.
const CookieName = "isrv_admin_session"

var encoding = base64.RawURLEncoding

// IssueToken returns a signed session token for username that expires after ttl.
// The token format is "<base64(username)>.<expiryUnix>.<base64(hmac)>".
func IssueToken(secret []byte, username string, ttl time.Duration) string {
	expiry := time.Now().Add(ttl).Unix()
	msg := encoding.EncodeToString([]byte(username)) + "." + strconv.FormatInt(expiry, 10)
	sig := sign(secret, msg)

	return msg + "." + encoding.EncodeToString(sig)
}

// Validate verifies the signature and expiry of token.
func Validate(secret []byte, token string) (string, bool) {
	if len(secret) == 0 {
		return "", false
	}

	usernameB64, rest, ok := strings.Cut(token, ".")
	if !ok {
		return "", false
	}

	expiryStr, sigB64, ok := strings.Cut(rest, ".")
	if !ok {
		return "", false
	}

	gotSig, err := encoding.DecodeString(sigB64)
	if err != nil {
		return "", false
	}

	msg := usernameB64 + "." + expiryStr
	wantSig := sign(secret, msg)
	if subtle.ConstantTimeCompare(gotSig, wantSig) != 1 {
		return "", false
	}

	usernameBytes, err := encoding.DecodeString(usernameB64)
	if err != nil {
		return "", false
	}

	expiry, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return "", false
	}

	if time.Now().Unix() >= expiry {
		return "", false
	}

	return string(usernameBytes), true
}

func sign(secret []byte, msg string) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(msg))

	return mac.Sum(nil)
}
