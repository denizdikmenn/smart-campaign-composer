package seed

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smart-campaign-composer/backend/internal/db"
	"github.com/smart-campaign-composer/backend/internal/dbtest"
)

func count(t *testing.T, pool *pgxpool.Pool, query string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSeedIfEmpty(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)

	for i := 0; i < 2; i++ { // Migrate is idempotent
		if err := db.Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}

	seeded, err := SeedIfEmpty(ctx, pool, 5000, testNow)
	if err != nil || !seeded {
		t.Fatalf("first seed: seeded=%v err=%v", seeded, err)
	}
	if n := count(t, pool, "SELECT count(*) FROM customers"); n != 5000 {
		t.Fatalf("customers = %d", n)
	}
	orders := count(t, pool, "SELECT count(*) FROM orders")
	carts := count(t, pool, "SELECT count(*) FROM cart_additions")
	if orders == 0 || carts == 0 {
		t.Fatalf("orders=%d carts=%d", orders, carts)
	}

	seeded, err = SeedIfEmpty(ctx, pool, 5000, testNow)
	if err != nil || seeded {
		t.Fatalf("second seed: seeded=%v err=%v", seeded, err)
	}
	if n := count(t, pool, "SELECT count(*) FROM orders"); n != orders {
		t.Fatalf("orders changed on re-seed: %d -> %d", orders, n)
	}

	// The identity sequence continues after the explicit ids.
	if _, err := pool.Exec(ctx, "INSERT INTO customers (first_name, last_name, email, city) VALUES ('A','B','a.b@example.com','Ankara')"); err != nil {
		t.Fatalf("insert after seed: %v", err)
	}
}

func TestSchemaConstraints(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	bad := map[string]string{
		"sms consent without phone":  "INSERT INTO customers (first_name,last_name,email,city,sms_consent) VALUES ('A','B','x@example.com','Ankara',true)",
		"uppercase email":            "INSERT INTO customers (first_name,last_name,email,city) VALUES ('A','B','X@example.com','Ankara')",
		"malformed phone":            "INSERT INTO customers (first_name,last_name,email,city,phone) VALUES ('A','B','y@example.com','Ankara','0532')",
		"order for unknown customer": "INSERT INTO orders (customer_id,category,total_amount,placed_at) VALUES (999,'toys',10,now())",
	}
	for name, q := range bad {
		_, err := pool.Exec(ctx, q)
		if _, ok := err.(*pgconn.PgError); !ok {
			t.Errorf("%s: expected a constraint error, got %v", name, err)
		}
	}

	// Channels are independent: email-only and SMS-only are both valid.
	good := "INSERT INTO customers (first_name,last_name,email,city,phone,email_consent,sms_consent) VALUES " +
		"('A','B','e@example.com','Ankara',NULL,true,false), ('C','D','s@example.com','Ankara','+905321234567',false,true)"
	if _, err := pool.Exec(ctx, good); err != nil {
		t.Errorf("valid consent combinations rejected: %v", err)
	}
}
