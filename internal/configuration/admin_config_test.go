package configuration

import (
	"testing"
	"time"

	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
)

func TestVerifyAdminConfig(t *testing.T) {
	t.Run("disabled leaves secret empty", func(t *testing.T) {
		original := config
		t.Cleanup(func() { config = original })

		config = getDefaultConfig()
		config.Admin = models.AdminConfiguration{}

		verifyAdminConfig()

		assert.False(t, config.Admin.Enabled())
		assert.Empty(t, config.Admin.SessionSecret)
	})

	t.Run("enabled generates secret and default TTL", func(t *testing.T) {
		original := config
		t.Cleanup(func() { config = original })

		config = getDefaultConfig()
		config.Admin = models.AdminConfiguration{Username: "admin", Password: "secret"}

		verifyAdminConfig()

		assert.True(t, config.Admin.Enabled())
		assert.NotEmpty(t, config.Admin.SessionSecret)
		assert.Equal(t, 24*time.Hour, config.Admin.SessionTTL)
	})

	t.Run("enabled preserves provided secret and TTL", func(t *testing.T) {
		original := config
		t.Cleanup(func() { config = original })

		config = getDefaultConfig()
		config.Admin = models.AdminConfiguration{
			Username:      "admin",
			Password:      "secret",
			SessionSecret: "fixed-secret",
			SessionTTL:    2 * time.Hour,
		}

		verifyAdminConfig()

		assert.Equal(t, "fixed-secret", config.Admin.SessionSecret)
		assert.Equal(t, 2*time.Hour, config.Admin.SessionTTL)
	})
}
