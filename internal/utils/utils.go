package utils

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

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

// GetIPAddress returns the real client IP, but only reads X-Forwarded-For
// and X-Real-IP headers when the direct connection (RemoteAddr) comes from one of
// the configured trustedProxies.
func GetIPAddress(r *http.Request, trustedProxies []string) string {
	remoteIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteIP = r.RemoteAddr
	}

	if len(trustedProxies) == 0 || !isInTrustedProxies(remoteIP, trustedProxies) {
		return remoteIP
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// XFF may be a comma-separated list; the leftmost entry is the original client.
		if ip := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0]); ip != "" {
			return ip
		}
	}

	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}

	return remoteIP
}

// isInTrustedProxies reports whether ip matches any entry in the list.
// Each entry may be a single IP address or a CIDR range (e.g. "10.0.0.0/8").
func isInTrustedProxies(ip string, trustedProxies []string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}

	for _, proxy := range trustedProxies {
		if strings.ContainsRune(proxy, '/') {
			_, network, err := net.ParseCIDR(proxy)
			if err == nil && network.Contains(parsed) {
				return true
			}
		} else if ip == proxy {
			return true
		}
	}

	return false
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
func RespondWithError(w http.ResponseWriter, code int, message string) error {
	errorData := make(map[string]string)
	errorData["error"] = message

	if err := setJsonResponse(w, code, errorData); err != nil {
		return fmt.Errorf("failed to write error response: %w", err)
	}

	return nil
}

// RespondWithSuccess sends a JSON success response and logs any write failures.
func RespondWithSuccess(w http.ResponseWriter, data any) error {
	if err := setJsonResponse(w, http.StatusOK, data); err != nil {
		return fmt.Errorf("failed to write success response: %w", err)
	}

	return nil
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

// SetStructField sets a field in the struct based on a dot-separated path.
// uses reflection to navigate the struct and set the value, converting types as needed.
//
// WARNING: Ugly function below! I'm sorry!
//
//nolint:gocognit,cyclop,funlen
func SetStructField(target any, fieldPath string, value any) error {
	parts := strings.Split(fieldPath, ".")
	current := target

	for i, part := range parts {
		v := reflect.ValueOf(current).Elem()
		field := reflect.Indirect(v).FieldByName(part)
		if !field.IsValid() {
			return fmt.Errorf("invalid field path: %s", fieldPath)
		}

		if i == len(parts)-1 {
			// Last part, set the value
			//
			//nolint:exhaustive
			switch field.Kind() {
			case reflect.String:
				if s, ok := value.(string); ok {
					field.SetString(s)
				} else {
					return fmt.Errorf("unsupported value type for string field: %T", value)
				}
			case reflect.Int:
				if n, ok := value.(int); ok {
					field.SetInt(int64(n))
				} else if s, ok := value.(string); ok {
					if n, err := strconv.Atoi(s); err == nil {
						field.SetInt(int64(n))
					} else {
						return fmt.Errorf("unsupported value type for int field: %T", value)
					}
				} else {
					return fmt.Errorf("unsupported value type for int field: %T", value)
				}
			case reflect.Bool:
				if b, ok := value.(bool); ok {
					field.SetBool(b)
				} else if s, ok := value.(string); ok {
					if b, err := strconv.ParseBool(s); err == nil {
						field.SetBool(b)
					} else {
						return fmt.Errorf("unsupported value type for bool field: %T", value)
					}
				} else {
					return fmt.Errorf("unsupported value type for bool field: %T", value)
				}
			case reflect.Int64:
				if field.Type() == reflect.TypeFor[time.Duration]() {
					if d, ok := value.(time.Duration); ok {
						field.Set(reflect.ValueOf(d))
					} else if s, ok := value.(string); ok {
						if d, err := time.ParseDuration(s); err == nil {
							field.Set(reflect.ValueOf(d))
						} else {
							return fmt.Errorf("unsupported value type for duration field: %T", value)
						}
					} else {
						return fmt.Errorf("unsupported value type for duration field: %T", value)
					}
				} else {
					if n, ok := value.(int64); ok {
						field.SetInt(n)
					} else if s, ok := value.(string); ok {
						if n, err := strconv.ParseInt(s, 10, 64); err == nil {
							field.SetInt(n)
						} else {
							return fmt.Errorf("unsupported value type for int64 field: %T", value)
						}
					} else {
						return fmt.Errorf("unsupported value type for int64 field: %T", value)
					}
				}
			default:
				return fmt.Errorf("unsupported field type: %s", field.Kind())
			}
		} else {
			current = field.Addr().Interface()
		}
	}

	return nil
}
