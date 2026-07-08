package storage

import (
	"testing"

	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_newS3Options(t *testing.T) {
	t.Run("aws endpoint resolved from region", func(t *testing.T) {
		opts := newS3Options(models.StorageConfiguration{
			Region:    "us-east-1",
			AccessKey: "key",
			SecretKey: "secret",
		})

		assert.Equal(t, "us-east-1", opts.Region)
		assert.Nil(t, opts.BaseEndpoint)
		assert.False(t, opts.UsePathStyle)
	})

	t.Run("custom endpoint uses path style", func(t *testing.T) {
		opts := newS3Options(models.StorageConfiguration{
			Region:    "auto",
			Endpoint:  "http://minio:9000",
			AccessKey: "key",
			SecretKey: "secret",
		})

		assert.Equal(t, "auto", opts.Region)
		require.NotNil(t, opts.BaseEndpoint)
		assert.Equal(t, "http://minio:9000", *opts.BaseEndpoint)
		assert.True(t, opts.UsePathStyle)
	})
}
