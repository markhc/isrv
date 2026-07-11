package database

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/markhc/isrv/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultTestPostgresDSN matches the credentials used by
// docker-compose.postgres.yaml for CI and manual runs.
const defaultTestPostgresDSN = "postgres://isrv:isrv@localhost:5432/isrv?sslmode=disable"

// connectTestPostgres connects to the Postgres instance addressed by
// ISRV_TEST_POSTGRES_DSN (falling back to the docker-compose defaults) and
// skips the test when it is unreachable.
func connectTestPostgres(t *testing.T) *PostgresDB {
	t.Helper()

	dsn := os.Getenv("ISRV_TEST_POSTGRES_DSN")
	if dsn == "" {
		dsn = defaultTestPostgresDSN
	}

	db := NewPostgresDB(models.Configuration{Database: models.DatabaseConfiguration{DSN: dsn}})
	require.NoError(t, db.Connect())
	t.Cleanup(func() { _ = db.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := db.Ping(ctx); err != nil {
		t.Skipf("Postgres is not reachable at %q, skipping: %v", dsn, err)
	}

	return db
}

func Test_PostgresDB_TryLock(t *testing.T) {
	db := connectTestPostgres(t)
	ctx := context.Background()

	release1, acquired1, err := db.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired1, "first locker should acquire the lock")
	require.NotNil(t, release1)

	// A second attempt runs on a different pooled connection, hence a
	// different Postgres session, and must not acquire the lock.
	release2, acquired2, err := db.TryLock(ctx)
	require.NoError(t, err)
	assert.False(t, acquired2, "second locker must not acquire while the first holds the lock")
	assert.Nil(t, release2)

	release1()

	// Once released, the lock is available again.
	release3, acquired3, err := db.TryLock(ctx)
	require.NoError(t, err)
	require.True(t, acquired3, "lock should be acquirable after release")
	release3()
}
