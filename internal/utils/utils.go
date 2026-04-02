package utils

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/markhc/isrv/internal/logging"
	"github.com/markhc/isrv/internal/models"
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

func CalculateExpirationTime(r *http.Request, fileSize int64, config *models.Configuration) time.Time {
	// Calculates the default expiration date for this file.
	// Expiration is based on file size, with larger files having shorter expiration times.
	// Expiration formula: min_age + (min_age - max_age) * pow((file_size / max_size - 1), 3)
	//
	// If a **shorter** time than the default is specified in the "expires" form field,
	// that time is used instead.
	maxSizeBytes := int64(config.MaxFileSizeMB * 1024 * 1024)
	minAge := int64(config.MinAgeDays * 24 * 3600 * 1000) // in milliseconds
	maxAge := int64(config.MaxAgeDays * 24 * 3600 * 1000) // in milliseconds

	defaultExpires := minAge + int64(float64(minAge-maxAge)*Pow3(float64(fileSize)/float64(maxSizeBytes)-1))
	defaultExpiresTime := time.Now().Add(time.Duration(defaultExpires) * time.Millisecond)

	if expiresStr := r.FormValue("expires"); expiresStr != "" {
		if expiresTime, err := ParseExpiresForm(expiresStr); err == nil {
			if expiresTime.Before(defaultExpiresTime) {
				return expiresTime
			}
		}
	}

	return defaultExpiresTime
}

// RespondWithError sends a JSON error response and logs any write failures.
func RespondWithError(w http.ResponseWriter, code int, message string) {
	errorData := make(map[string]string)
	errorData["error"] = message

	if err := setJsonResponse(w, code, errorData); err != nil {
		logging.LogError("failed to write error response", logging.Error(err))
	}
}

// RespondWithSuccess sends a JSON success response and logs any write failures.
func RespondWithSuccess(w http.ResponseWriter, data any) {
	if err := setJsonResponse(w, http.StatusOK, data); err != nil {
		logging.LogError("failed to write success response", logging.Error(err))
	}
}

func setJsonResponse(w http.ResponseWriter, statusCode int, data any) error {
	w.Header().Set("Content-Type", "application/json")

	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON response: %w", err)
	}

	w.WriteHeader(statusCode)

	_, err = w.Write(b)
	if err != nil {
		return fmt.Errorf("failed to write JSON response: %w", err)
	}

	return nil
}
