package utils

import (
	cryptoRand "crypto/rand"
	"encoding/hex"
	"fmt"
	mathRand "math/rand"
	"net"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/markhc/isrv/internal/models"
)

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// GenerateFileToken returns a cryptographically random token used to
// authenticate management operations on an uploaded file.
func GenerateFileToken() (string, error) {
	const length = 16

	b := make([]byte, length)
	if _, err := cryptoRand.Read(b); err != nil {
		return "", fmt.Errorf("failed to get random bytes: %w", err)
	}

	return hex.EncodeToString(b), nil
}

// GenerateRandomString returns a random alphanumeric string of the given
// length. It is not suitable for security-sensitive use.
func GenerateRandomString(length int) string {
	data := make([]byte, length)
	for i := range data {
		data[i] = charset[mathRand.Intn(len(charset))] // #nosec G404 -- non-security use
	}

	return string(data)
}

// Pow3 returns x raised to the power of 3.
func Pow3(x float64) float64 {
	return x * x * x
}

// ParseExpiresForm parses the "expires" form field value into a time.Time.
// Values below 1,000,000 are interpreted as a duration in hours from now;
// larger values are interpreted as a Unix timestamp in milliseconds.
func ParseExpiresForm(expiresStr string) (time.Time, error) {
	var expires int64
	var err error

	expires, err = strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid expires value: %w", err)
	}

	if expires < 1000000 {
		expires = expires * 3600 * 1000
		expiresTime := time.Now().Add(time.Duration(expires) * time.Millisecond)

		return expiresTime, nil
	}

	return time.UnixMilli(expires), nil
}

// GetIPAddress returns the resolved client IP for c. X-Forwarded-For and
// X-Real-IP are consulted only when the immediate peer is contained in
// trustedProxies; otherwise the peer address is returned.
func GetIPAddress(c fiber.Ctx, trustedProxies []string) string {
	return resolveIPAddress(c.IP(), func(k string) string { return c.Get(k) }, trustedProxies)
}

// resolveIPAddress is the testable core of GetIPAddress.
func resolveIPAddress(remoteIP string, getHeader func(string) string, trustedProxies []string) string {
	if len(trustedProxies) == 0 || !isInTrustedProxies(remoteIP, trustedProxies) {
		return remoteIP
	}

	if xff := getHeader("X-Forwarded-For"); xff != "" {
		// XFF may be a comma-separated list; the leftmost entry is the original client.
		if ip := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0]); ip != "" {
			return ip
		}
	}

	if xri := strings.TrimSpace(getHeader("X-Real-IP")); xri != "" {
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
		} else {
			proxyIP := net.ParseIP(proxy)
			if proxyIP != nil && proxyIP.Equal(parsed) {
				return true
			}
		}
	}

	return false
}

// CalculateExpirationTime returns the expiration time for the uploaded file.
//
// The default is derived from the file size: larger files expire sooner,
// following minAge + (minAge - maxAge) * pow(size/maxSize - 1, 3).
// If the request's "expires" form field requests a sooner expiration, that
// value is used instead.
func CalculateExpirationTime(c fiber.Ctx, fileSize int64, config *models.Configuration) time.Time {
	defaultExpiresTime := MaxExpirationTime(fileSize, config)

	if expiresStr := c.FormValue("expires"); expiresStr != "" {
		if expiresTime, err := ParseExpiresForm(expiresStr); err == nil {
			if expiresTime.Before(defaultExpiresTime) {
				return expiresTime
			}
		}
	}

	return defaultExpiresTime
}

// MaxExpirationTime returns the maximum allowed expiration time for a file
// of the given size, computed using the same size-based formula as the
// default upload expiry.
func MaxExpirationTime(fileSize int64, config *models.Configuration) time.Time {
	maxSizeBytes := int64(config.MaxFileSizeMB * 1024 * 1024)
	minAge := int64(config.MinAgeDays * 24 * 3600 * 1000)
	maxAge := int64(config.MaxAgeDays * 24 * 3600 * 1000)

	defaultExpires := minAge + int64(float64(minAge-maxAge)*Pow3(float64(fileSize)/float64(maxSizeBytes)-1))

	return time.Now().Add(time.Duration(defaultExpires) * time.Millisecond)
}

// RespondWithError writes a JSON error payload with the given status code.
func RespondWithError(c fiber.Ctx, code int, message string) error {
	if err := c.Status(code).JSON(map[string]string{"error": message}); err != nil {
		return fmt.Errorf("failed to write error response: %w", err)
	}

	return nil
}

// RespondWithSuccess writes a JSON success payload with HTTP 200.
func RespondWithSuccess(c fiber.Ctx, data any) error {
	if err := c.Status(fiber.StatusOK).JSON(data); err != nil {
		return fmt.Errorf("failed to write success response: %w", err)
	}

	return nil
}

// SetStructField assigns value to the field of target identified by the
// dot-separated fieldPath, using reflection to walk into nested structs and
// converting from string where needed.
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

func GetFileExtension(filename string) string {
	if dot := strings.LastIndex(filename, "."); dot != -1 && dot < len(filename)-1 {
		return filename[dot:]
	}

	return ""
}
