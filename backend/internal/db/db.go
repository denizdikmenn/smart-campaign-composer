// Package db owns the database schema.
package db

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schema string

// Migrate creates the schema if it does not exist yet. The schema file is
// idempotent, so this is safe to call on every startup.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// No arguments => simple protocol, which allows multiple statements.
	_, err := pool.Exec(ctx, schema)
	return err
}
