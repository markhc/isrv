package utils

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
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

// calcExpiresCtx fires a POST with the given expires form field value through
// a minimal Fiber app and returns the time computed by CalculateExpirationTime.
// Pass an empty string to omit the field entirely.
func calcExpiresCtx(t *testing.T, expiresVal string, fileSize int64, cfg *models.Configuration) time.Time {
	t.Helper()
	var result time.Time
	app := fiber.New()
	app.Post("/", func(c fiber.Ctx) error {
		result = CalculateExpirationTime(c, fileSize, cfg)
		return c.SendString("ok")
	})

	var body io.Reader
	var contentType string
	if expiresVal != "" {
		body = strings.NewReader("expires=" + url.QueryEscape(expiresVal))
		contentType = "application/x-www-form-urlencoded"
	}

	req := httptest.NewRequest(http.MethodPost, "/", body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := app.Test(req)
	require.NoError(t, err)
	resp.Body.Close()
	return result
}

func Test_CalculateExpirationTime(t *testing.T) {
	cfg := &models.Configuration{
		MaxFileSizeMB: 100,
		MinAgeDays:    30,
		MaxAgeDays:    365,
	}

	t.Run("no expires param uses formula", func(t *testing.T) {
		before := time.Now()
		exp := calcExpiresCtx(t, "", 0, cfg)
		after := time.Now()

		assert.True(t, exp.After(before))
		assert.True(t, exp.Before(after.Add(time.Duration(cfg.MaxAgeDays)*24*time.Hour+time.Second)))
	})

	t.Run("expires unix ms earlier than default is used", func(t *testing.T) {
		// value < 1000000 is treated as hours; use a large unix ms value
		target := time.Now().Add(1 * time.Hour)
		exp := calcExpiresCtx(t, fmt.Sprintf("%d", target.UnixMilli()), 0, cfg)

		assert.WithinDuration(t, target, exp, 2*time.Second)
	})

	t.Run("expires unix ms later than default falls back to default", func(t *testing.T) {
		farFuture := time.Now().Add(10 * 365 * 24 * time.Hour)
		exp := calcExpiresCtx(t, fmt.Sprintf("%d", farFuture.UnixMilli()), 0, cfg)

		assert.True(t, exp.Before(farFuture))
	})

	t.Run("invalid expires string falls back to default", func(t *testing.T) {
		before := time.Now()
		exp := calcExpiresCtx(t, "notanumber", 0, cfg)

		assert.True(t, exp.After(before))
	})

	t.Run("expires in hours is interpreted as hours", func(t *testing.T) {
		// value < 1000000 is treated as hours
		exp := calcExpiresCtx(t, "2", 0, cfg)
		target := time.Now().Add(2 * time.Hour)

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

func Test_SetStructField_invalidPath(t *testing.T) {
	type TestStruct struct {
		IntField int
	}

	ts := &TestStruct{IntField: 42}
	err := SetStructField(ts, "NonExistentField", 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid field path")
	assert.Equal(t, 42, ts.IntField) // should remain unchanged
}

func Test_SetStructField_invalidNestedPath(t *testing.T) {
	type TestStruct struct {
		Nested struct {
			IntField int
		}
	}

	ts := &TestStruct{}
	err := SetStructField(ts, "Nested.Missing", 100)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid field path")
}

// textField implements encoding.TextUnmarshaler so SetStructField's
// TextUnmarshaler branch (which takes precedence over kind-based handling) can
// be exercised.
type textField struct {
	val string
}

func (t *textField) UnmarshalText(b []byte) error {
	if string(b) == "boom" {
		return fmt.Errorf("refusing to unmarshal %q", b)
	}
	t.val = strings.ToUpper(string(b))
	return nil
}

func Test_SetStructField_textUnmarshaler(t *testing.T) {
	type TestStruct struct {
		Field textField
	}

	t.Run("string uses UnmarshalText", func(t *testing.T) {
		ts := &TestStruct{}
		err := SetStructField(ts, "Field", "hello")
		require.NoError(t, err)
		assert.Equal(t, "HELLO", ts.Field.val)
	})

	t.Run("UnmarshalText error is propagated", func(t *testing.T) {
		ts := &TestStruct{}
		err := SetStructField(ts, "Field", "boom")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse value for field")
	})

	t.Run("non-string value skips UnmarshalText and hits kind switch", func(t *testing.T) {
		// A struct kind with no matching switch case falls through to the
		// default "unsupported field type" branch when value is not a string.
		ts := &TestStruct{}
		err := SetStructField(ts, "Field", 123)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported field type")
	})
}

func Test_SetStructField_stringFieldNonStringValue(t *testing.T) {
	type TestStruct struct {
		StringField string
	}

	ts := &TestStruct{StringField: "keep"}
	err := SetStructField(ts, "StringField", 42)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported value type for string field")
	assert.Equal(t, "keep", ts.StringField) // unchanged
}

func Test_SetStructField_intFromString(t *testing.T) {
	type TestStruct struct {
		IntField int
	}

	ts := &TestStruct{}
	err := SetStructField(ts, "IntField", "42")
	require.NoError(t, err)
	assert.Equal(t, 42, ts.IntField)
}

func Test_SetStructField_int64FromString(t *testing.T) {
	type TestStruct struct {
		Int64Field int64
	}

	ts := &TestStruct{}
	err := SetStructField(ts, "Int64Field", "9000000000")
	require.NoError(t, err)
	assert.Equal(t, int64(9000000000), ts.Int64Field)
}

func Test_SetStructField_unsupportedFieldType(t *testing.T) {
	type TestStruct struct {
		FloatField float64
	}

	ts := &TestStruct{}
	err := SetStructField(ts, "FloatField", 3.14)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported field type")
}

func Test_GenerateFileToken(t *testing.T) {
	tok, err := GenerateFileToken()
	require.NoError(t, err)

	// 16 random bytes hex-encoded => 32 hex characters.
	assert.Len(t, tok, 32)
	for _, r := range tok {
		assert.True(t, strings.ContainsRune("0123456789abcdef", r), "non-hex char %c in token", r)
	}

	// Repeated calls should differ.
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		next, err := GenerateFileToken()
		require.NoError(t, err)
		assert.False(t, seen[next], "GenerateFileToken produced a duplicate: %q", next)
		seen[next] = true
	}
}

func Test_GetFileExtension(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{"simple extension", "file.txt", ".txt"},
		{"multiple dots", "archive.tar.gz", ".gz"},
		{"no extension", "README", ""},
		{"trailing dot", "file.", ""},
		{"leading dot only", ".hidden", ".hidden"},
		{"empty string", "", ""},
		{"path with extension", "dir/sub/file.png", ".png"},
		{"uppercase extension", "IMAGE.PNG", ".PNG"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, GetFileExtension(tt.filename))
		})
	}
}

func Test_RespondWithError(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		return RespondWithError(c, fiber.StatusBadRequest, "something broke")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, fiber.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), fiber.MIMEApplicationJSON)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"error":"something broke"}`, string(body))
}

func Test_RespondWithSuccess(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		return RespondWithSuccess(c, map[string]any{"id": "abc", "count": 3})
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), fiber.MIMEApplicationJSON)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"abc","count":3}`, string(body))
}
