// Package db owns the database schema.
package db

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

// migrateLock serializes migrations between processes (API and worker both
// migrate on startup).
const migrateLock = 7236001

// Migrate creates the schema if it does not exist yet. The schema file is
// idempotent, so this is safe to call on every startup.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// No arguments => simple protocol, which allows multiple statements. The
	// statements run as one implicit transaction, so the transaction-level
	// advisory lock is held until the whole schema is applied.
	_, err := pool.Exec(ctx, fmt.Sprintf("SELECT pg_advisory_xact_lock(%d);\n", migrateLock)+schema)
	return err
}
