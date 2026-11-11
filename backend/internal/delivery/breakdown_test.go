package delivery

import (
	"context"
	"testing"

	"github.com/smart-campaign-composer/backend/internal/db"
	"github.com/smart-campaign-composer/backend/internal/dbtest"
)

func TestBreakdown(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO customers (id, first_name, last_name, email, city) SELECT g, 'F', 'L', 'c' || g || '@example.com', 'Istanbul' FROM generate_series(1, 4) g;
		INSERT INTO campaigns (id, brief, audience) VALUES (1, 'a brief text', '{}');
		INSERT INTO deliveries (campaign_id, customer_id, channel, variant_position, language, content_source, body, status, attempts) VALUES
		(1, 1, 'sms',   1, 'tr', 'template', 'x', 'sent', 1),
		(1, 1, 'email', 1, 'tr', 'template', 'x', 'sent', 1),
		(1, 2, 'sms',   2, 'en', 'template', 'x', 'failed', 3),
		(1, 3, 'sms',   1, 'tr', 'template', 'x', 'sent', 2),
		(1, 4, 'sms',   2, 'tr', 'template', 'x', 'queued', 0);`); err != nil {
		t.Fatal(err)
	}
	// A retry bumps attempts and status but never the assignment.
	if _, err := pool.Exec(ctx, `UPDATE deliveries SET attempts = attempts + 1, status = 'sent' WHERE customer_id = 4`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deliveries (campaign_id, customer_id, channel, variant_position, language, content_source, body)
		VALUES (1, 1, 'sms', 2, 'tr', 'template', 'dup')`); err == nil {
		t.Fatal("a recipient/channel must have exactly one delivery record")
	}
	rows, err := Breakdown(ctx, pool, 1)
	if err != nil {
		t.Fatal(err)
	}
	type key struct {
		variant         int
		channel, status string
	}
	got := map[key]Row{}
	total := 0
	for _, r := range rows {
		got[key{*r.VariantPosition, r.Channel, r.Status}] = r
		total += r.Messages
	}
	if total != 5 {
		t.Fatalf("messages %d, rows %+v", total, rows)
	}
	if r := got[key{1, "sms", "sent"}]; r.Messages != 2 || r.Retries != 1 {
		t.Fatalf("variant A sms sent: %+v", r)
	}
	if r := got[key{2, "sms", "failed"}]; r.Messages != 1 || r.Retries != 2 {
		t.Fatalf("variant B sms failed: %+v", r)
	}
	if r := got[key{2, "sms", "sent"}]; r.Messages != 1 {
		t.Fatalf("variant B retried message must stay in B: %+v", r)
	}
}
