package utils

import (
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// GenerateRandomString returns a random alphanumeric string of the given length.
func GenerateRandomString(length int) string {
	data := make([]byte, length)
	for i := range length {
		data[i] = charset[rand.Intn(len(charset))] // #nosec G404 -- This is not used for security purposes
	}

	return string(data)
}

// Pow3 returns x raised to the power of 3.
func Pow3(x float64) float64 {
	return x * x * x
}

// ParseExpiresForm parses the "expires" form field value into a time.Time.
// The value may be either a duration in hours (small integers) or a Unix
// timestamp in milliseconds.
func ParseExpiresForm(expiresStr string) (time.Time, error) {
	var expires int64
	var err error

	expires, err = strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid expires value: %w", err)
	}

	// If the value is less than 1,000,000, assume it's in hours
	if expires < 1000000 {
		expires = expires * 3600 * 1000 // convert hours to milliseconds
		expiresTime := time.Now().Add(time.Duration(expires) * time.Millisecond)

		return expiresTime, nil
	}

	return time.UnixMilli(expires), nil
}

// GetIPAddress returns the client IP address from the request, respecting
// X-Forwarded-For and X-Real-IP proxy headers when present.
func GetIPAddress(r *http.Request) string {
	fwdAddress := r.Header.Get("X-Forwarded-For")
	if fwdAddress != "" {
		ips := strings.Split(fwdAddress, ", ")
		if len(ips) > 1 {
			return ips[0]
		} else {
			return fwdAddress
		}
	}
	ip := r.Header.Get("X-Real-IP")
	if ip != "" {
		return ip
	}

	// No proxy headers, return the remote address from the request
	// Note: r.RemoteAddr may include the port number
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return ip
}
