package utils

import (
	"math/rand"
	"net/http"
	"strconv"
	"strings"
)

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func GenerateRandomString(length int) string {
	var data = make([]byte, length)
	for i := 0; i < length; i++ {
		data[i] = charset[rand.Intn(len(charset))]
	}
	return string(data)
}

func Pow3(x float64) float64 {
	return x * x * x
}

func ParseExpiresForm(expiresStr string) (int64, error) {
	// Parses the "expires" form field which can be in the format of
	// either an integer number of hours or milliseconds since UNIX epoch.

	var expires int64
	var err error

	expires, err = strconv.ParseInt(expiresStr, 10, 64)
	if err != nil {
		return 0, err
	}

	// If the value is less than 1,000,000, assume it's in hours
	if expires < 1000000 {
		expires = expires * 3600 * 1000 // convert hours to milliseconds
	}

	return expires, nil
}

func GetIPAddress(r *http.Request) string {
	// Get the client's IP address from the request, accounting for proxies
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
	ip = r.RemoteAddr
	if colonIndex := strings.LastIndex(ip, ":"); colonIndex != -1 && strings.Count(ip, ":") == 1 {
		ip = ip[:colonIndex]
	}
	return ip
}
