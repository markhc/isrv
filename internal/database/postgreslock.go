package database

import (
	"context"
	"database/sql/driver"
	"fmt"
	"time"
)

// cleanupCycleLockKey is the advisory lock key that serializes cleanup
// cycles across replicas sharing one Postgres database. The value is
// arbitrary ("isrv_cln" as a 64-bit integer) but must be consistent across all replicas.
const cleanupCycleLockKey int64 = 0x697372765f636c6e

// timeout for lock and unlock operations.
const advisoryLockOpTimeout = 10 * time.Second

// TryLock implements the cleanup.CycleLocker capability using a Postgres
// session-scoped advisory lock.
//
// On success it returns acquired=true and a release function that unlocks and
// returns the pinned connection; when another session holds the lock it
// returns acquired=false with no error.
func (db *PostgresDB) TryLock(ctx context.Context) (func(), bool, error) {
	lockCtx, cancel := context.WithTimeout(ctx, advisoryLockOpTimeout)
	defer cancel()

	conn, err := db.sqldb.Conn(lockCtx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to obtain connection for cleanup advisory lock: %w", err)
	}

	var locked bool

	err = conn.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock($1)", cleanupCycleLockKey).Scan(&locked)
	if err != nil {
		_ = conn.Close()

		return nil, false, fmt.Errorf("failed to acquire cleanup advisory lock: %w", err)
	}

	if !locked {
		_ = conn.Close()

		return nil, false, nil
	}

	release := func() {
		// The unlock must run on the same pinned connection that took the lock
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), advisoryLockOpTimeout)
		defer cancel()

		_, unlockErr := conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", cleanupCycleLockKey)
		if unlockErr != nil {
			// Failed to release the lock. Just give up and let Postgres clean it up when the session ends.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}

		_ = conn.Close()
	}

	return release, true, nil
}
