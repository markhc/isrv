package utils

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_GenerateRandomString_length(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"zero length", 0},
		{"single char", 1},
		{"small string", 8},
		{"medium string", 32},
		{"large string", 256},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GenerateRandomString(tt.length)
			assert.Len(t, result, tt.length)

			for _, char := range result {
				assert.True(t, strings.ContainsRune(charset, char), "invalid character %c in result", char)
			}
		})
	}
}

func Test_GenerateRandomString_uniqueness(t *testing.T) {
	const length = 16
	const iterations = 1000

	seen := make(map[string]bool)
	for i := 0; i < iterations; i++ {
		result := GenerateRandomString(length)
		assert.False(t, seen[result], "GenerateRandomString(%d) generated duplicate: %q", length, result)
		seen[result] = true
	}
}

func Test_Pow3(t *testing.T) {
	tests := []struct {
		name string
		x    float64
		want float64
	}{
		{"zero", 0, 0},
		{"positive integer", 2, 8},
		{"negative integer", -3, -27},
		{"decimal", 1.5, 3.375},
		{"negative decimal", -2.5, -15.625},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Pow3(tt.x)
			assert.Equal(t, tt.want, result)
		})
	}
}

func Test_ParseExpiresForm_hours(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantHours int64
		wantErr   bool
	}{
		{"one hour", "1", 1, false},
		{"24 hours", "24", 24, false},
		{"zero hours", "0", 0, false},
		{"large hours", "8760", 8760, false}, // 1 year
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			result, err := ParseExpiresForm(tt.input)
			after := time.Now()

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			expectedDuration := time.Duration(tt.wantHours) * time.Hour
			assert.True(t, !result.Before(before.Add(expectedDuration)) && !result.After(after.Add(expectedDuration)),
				"ParseExpiresForm(%q) = %v, want between %v and %v", tt.input, result, before.Add(expectedDuration), after.Add(expectedDuration))
		})
	}
}

func Test_ParseExpiresForm_unixMillis(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantUnixMs int64
		wantErr    bool
	}{
		{"unix timestamp", "1640995200000", 1640995200000, false},   // Jan 1, 2022
		{"larger timestamp", "1893456000000", 1893456000000, false}, // Jan 1, 2030
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseExpiresForm(tt.input)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, time.UnixMilli(tt.wantUnixMs), result)
		})
	}
}

func Test_ParseExpiresForm_invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"non-numeric", "abc"},
		{"decimal", "12.5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseExpiresForm(tt.input)
			assert.Error(t, err)
		})
	}
}

func Test_GetIPAddress_xForwardedFor(t *testing.T) {
	tests := []struct {
		name         string
		forwardedFor string
		expectedIP   string
	}{
		{"single IP", "192.168.1.1", "192.168.1.1"},
		{"multiple IPs", "192.168.1.1, 10.0.0.1, 172.16.0.1", "192.168.1.1"},
		{"single IP with spaces", "  203.0.113.1  ", "  203.0.113.1  "}, // preserves original behavior
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("X-Forwarded-For", tt.forwardedFor)

			result := GetIPAddress(req)
			assert.Equal(t, tt.expectedIP, result)
		})
	}
}

func Test_GetIPAddress_xRealIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Real-IP", "198.51.100.1")
	req.RemoteAddr = "127.0.0.1:12345"

	result := GetIPAddress(req)
	assert.Equal(t, "198.51.100.1", result)
}

func Test_GetIPAddress_remoteAddr(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		expectedIP string
	}{
		{"IPv4 with port", "192.168.1.100:54321", "192.168.1.100"},
		{"IPv4 without port", "10.0.0.5", "10.0.0.5"},
		{"IPv6 with port", "[::1]:8080", "::1"},
		{"IPv6 without port", "2001:db8::1", "2001:db8::1"},
		{"localhost with port", "127.0.0.1:9000", "127.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remoteAddr

			result := GetIPAddress(req)
			assert.Equal(t, tt.expectedIP, result)
		})
	}
}

func Test_GetIPAddress_precedence(t *testing.T) {
	// X-Forwarded-For should take precedence over X-Real-IP and RemoteAddr
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	req.Header.Set("X-Real-IP", "198.51.100.10")
	req.RemoteAddr = "127.0.0.1:8080"

	result := GetIPAddress(req)
	assert.Equal(t, "203.0.113.5", result)

	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("X-Real-IP", "198.51.100.20")
	req2.RemoteAddr = "127.0.0.1:9090"

	result2 := GetIPAddress(req2)
	assert.Equal(t, "198.51.100.20", result2)
}

func Test_CalculateExpirationTime(t *testing.T) {
	cfg := &models.Configuration{
		MaxFileSizeMB: 100,
		MinAgeDays:    30,
		MaxAgeDays:    365,
	}

	t.Run("no expires param uses formula", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		before := time.Now()
		exp := CalculateExpirationTime(req, 0, cfg)
		after := time.Now()

		assert.True(t, exp.After(before))
		assert.True(t, exp.Before(after.Add(time.Duration(cfg.MaxAgeDays)*24*time.Hour+time.Second)))
	})

	t.Run("expires unix ms earlier than default is used", func(t *testing.T) {
		// value < 1000000 is treated as hours; use a large unix ms value
		target := time.Now().Add(1 * time.Hour)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/?expires=%d", target.UnixMilli()), nil)
		exp := CalculateExpirationTime(req, 0, cfg)

		assert.WithinDuration(t, target, exp, 2*time.Second)
	})

	t.Run("expires unix ms later than default falls back to default", func(t *testing.T) {
		farFuture := time.Now().Add(10 * 365 * 24 * time.Hour)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/?expires=%d", farFuture.UnixMilli()), nil)
		exp := CalculateExpirationTime(req, 0, cfg)

		assert.True(t, exp.Before(farFuture))
	})

	t.Run("invalid expires string falls back to default", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/?expires=notanumber", nil)
		before := time.Now()
		exp := CalculateExpirationTime(req, 0, cfg)

		assert.True(t, exp.After(before))
	})

	t.Run("expires in hours is interpreted as hours", func(t *testing.T) {
		// value < 1000000 is treated as hours
		req := httptest.NewRequest(http.MethodPost, "/?expires=2", nil)
		target := time.Now().Add(2 * time.Hour)
		exp := CalculateExpirationTime(req, 0, cfg)

		assert.WithinDuration(t, target, exp, 2*time.Second)
	})
}

func Test_SetStructField_integer(t *testing.T) {
	type TestStruct struct {
		IntField int
	}

	ts := &TestStruct{}
	err := SetStructField(ts, "IntField", 42)
	assert.NoError(t, err)
	assert.Equal(t, 42, ts.IntField)
}

func Test_SetStructField_string(t *testing.T) {
	type TestStruct struct {
		StringField string
	}

	ts := &TestStruct{}
	err := SetStructField(ts, "StringField", "hello")
	assert.NoError(t, err)
	assert.Equal(t, "hello", ts.StringField)
}

func Test_SetStructField_bool(t *testing.T) {
	type TestStruct struct {
		BoolField bool
	}

	ts := &TestStruct{}

	t.Run("true value", func(t *testing.T) {
		err := SetStructField(ts, "BoolField", true)
		assert.NoError(t, err)
		assert.Equal(t, true, ts.BoolField)
	})

	t.Run("false value", func(t *testing.T) {
		err := SetStructField(ts, "BoolField", false)
		assert.NoError(t, err)
		assert.Equal(t, false, ts.BoolField)
	})

	t.Run("string value", func(t *testing.T) {
		err := SetStructField(ts, "BoolField", "true")
		assert.NoError(t, err)
		assert.Equal(t, true, ts.BoolField)
	})

	t.Run("invalid type (string)", func(t *testing.T) {
		err := SetStructField(ts, "BoolField", "notabool")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported value type for bool field")
		assert.Equal(t, true, ts.BoolField) // should remain unchanged
	})

	t.Run("invalid type (int)", func(t *testing.T) {
		err := SetStructField(ts, "BoolField", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported value type for bool field")
		assert.Equal(t, true, ts.BoolField) // should remain unchanged
	})
}

func Test_SetStructField_duration(t *testing.T) {
	type TestStruct struct {
		DurationField time.Duration
	}

	ts := &TestStruct{}

	t.Run("valid duration", func(t *testing.T) {
		err := SetStructField(ts, "DurationField", 2*time.Hour)
		assert.NoError(t, err)
		assert.Equal(t, 2*time.Hour, ts.DurationField)
	})

	t.Run("negative duration", func(t *testing.T) {
		err := SetStructField(ts, "DurationField", -30*time.Minute)
		assert.NoError(t, err)
		assert.Equal(t, -30*time.Minute, ts.DurationField)
	})

	t.Run("valid duration string", func(t *testing.T) {
		err := SetStructField(ts, "DurationField", "4h")
		assert.NoError(t, err)
		assert.Equal(t, 4*time.Hour, ts.DurationField)
	})

	t.Run("negative duration string", func(t *testing.T) {
		err := SetStructField(ts, "DurationField", "-1h")
		assert.NoError(t, err)
		assert.Equal(t, -1*time.Hour, ts.DurationField)
	})

	t.Run("invalid type (string)", func(t *testing.T) {
		err := SetStructField(ts, "DurationField", "notaduration")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported value type for duration field")
		assert.Equal(t, -1*time.Hour, ts.DurationField) // should remain unchanged
	})

	t.Run("invalid type (int)", func(t *testing.T) {
		err := SetStructField(ts, "DurationField", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported value type for duration field")
		assert.Equal(t, -1*time.Hour, ts.DurationField) // should remain unchanged
	})
}

func Test_SetStructField_int(t *testing.T) {
	type TestStruct struct {
		IntField int
	}

	ts := &TestStruct{}

	t.Run("valid int64", func(t *testing.T) {
		err := SetStructField(ts, "IntField", int(42))
		assert.NoError(t, err)
		assert.Equal(t, int(42), ts.IntField)
	})

	t.Run("negative int", func(t *testing.T) {
		err := SetStructField(ts, "IntField", int(-42))
		assert.NoError(t, err)
		assert.Equal(t, int(-42), ts.IntField)
	})

	t.Run("invalid type (string)", func(t *testing.T) {
		err := SetStructField(ts, "IntField", "notanint")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported value type for int field")
		assert.Equal(t, int(-42), ts.IntField) // should remain unchanged
	})
}

func Test_SetStructField_int64(t *testing.T) {
	type TestStruct struct {
		Int64Field int64
	}

	ts := &TestStruct{}

	t.Run("valid int64", func(t *testing.T) {
		err := SetStructField(ts, "Int64Field", int64(42))
		assert.NoError(t, err)
		assert.Equal(t, int64(42), ts.Int64Field)
	})

	t.Run("negative int64", func(t *testing.T) {
		err := SetStructField(ts, "Int64Field", int64(-42))
		assert.NoError(t, err)
		assert.Equal(t, int64(-42), ts.Int64Field)
	})

	t.Run("invalid type (string)", func(t *testing.T) {
		err := SetStructField(ts, "Int64Field", "notanint64")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported value type for int64 field")
		assert.Equal(t, int64(-42), ts.Int64Field) // should remain unchanged
	})

	t.Run("invalid type (int)", func(t *testing.T) {
		err := SetStructField(ts, "Int64Field", 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported value type for int64 field")
		assert.Equal(t, int64(-42), ts.Int64Field) // should remain unchanged
	})
}

func Test_SetStructField_dotpaths(t *testing.T) {
	type TestStruct struct {
		Nested struct {
			IntField int
		}
	}

	ts := &TestStruct{}

	t.Run("valid int64", func(t *testing.T) {
		err := SetStructField(ts, "Nested.IntField", int(42))
		assert.NoError(t, err)
		assert.Equal(t, int(42), ts.Nested.IntField)
	})

	t.Run("invalid type (string)", func(t *testing.T) {
		err := SetStructField(ts, "Nested.IntField", "notanint")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported value type for int field")
		assert.Equal(t, int(42), ts.Nested.IntField) // should remain unchanged
	})
}
