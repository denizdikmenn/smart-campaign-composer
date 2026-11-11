package seed

import (
	"context"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SeedIfEmpty inserts n generated customers (with activity) when the customers
// table is empty and reports whether it did. It is safe to call on every start.
func SeedIfEmpty(ctx context.Context, pool *pgxpool.Pool, n int, now time.Time) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	// Serialises concurrent starters so only one of them seeds.
	if _, err := tx.Exec(ctx, "LOCK TABLE customers IN EXCLUSIVE MODE"); err != nil {
		return false, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM customers)").Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	ds := Generate(n, now)

	_, err = tx.CopyFrom(ctx, pgx.Identifier{"customers"},
		[]string{"id", "first_name", "last_name", "email", "phone", "city", "language", "email_consent", "sms_consent", "created_at", "last_active_at"},
		pgx.CopyFromSlice(len(ds.Customers), func(i int) ([]any, error) {
			c := ds.Customers[i]
			return []any{c.ID, c.FirstName, c.LastName, c.Email, c.Phone, c.City, c.Language, c.EmailConsent, c.SMSConsent, c.CreatedAt, c.LastActiveAt}, nil
		}))
	if err != nil {
		return false, err
	}

	// Explicit ids were used above; move the identity sequence past them.
	if _, err := tx.Exec(ctx, "SELECT setval(pg_get_serial_sequence('customers', 'id'), (SELECT max(id) FROM customers))"); err != nil {
		return false, err
	}

	_, err = tx.CopyFrom(ctx, pgx.Identifier{"orders"},
		[]string{"customer_id", "category", "total_amount", "placed_at"},
		pgx.CopyFromSlice(len(ds.Orders), func(i int) ([]any, error) {
			o := ds.Orders[i]
			amount := pgtype.Numeric{Int: big.NewInt(o.TotalCents), Exp: -2, Valid: true}
			return []any{o.CustomerID, o.Category, amount, o.PlacedAt}, nil
		}))
	if err != nil {
		return false, err
	}

	_, err = tx.CopyFrom(ctx, pgx.Identifier{"cart_additions"},
		[]string{"customer_id", "category", "added_at"},
		pgx.CopyFromSlice(len(ds.CartAdditions), func(i int) ([]any, error) {
			a := ds.CartAdditions[i]
			return []any{a.CustomerID, a.Category, a.AddedAt}, nil
		}))
	if err != nil {
		return false, err
	}

	return true, tx.Commit(ctx)
}
