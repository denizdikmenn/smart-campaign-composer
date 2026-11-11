package segmentation

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/smart-campaign-composer/backend/internal/db"
	"github.com/smart-campaign-composer/backend/internal/dbtest"
)

type refusingGen struct{}

func (refusingGen) Source() string { return "claude" }
func (refusingGen) Generate(context.Context, string, Catalog, time.Time) (GenResult, error) {
	return GenResult{Raw: "{}", InputTokens: 10, OutputTokens: 2}, ErrRefusal
}

// TestSuggestIsTraced checks that success and failure each leave one row.
func TestSuggestIsTraced(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO customers (first_name, last_name, email, city) VALUES ('A','A','a@example.com','Istanbul');
		INSERT INTO orders (customer_id, category, total_amount, placed_at)
		SELECT id, 'fashion', 10, now() FROM customers`); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)

	resp, err := NewService(pool, Mock{}, log).Suggest(ctx, "Istanbul cart abandoners please")
	if err != nil || resp.RunID == 0 {
		t.Fatalf("suggest: err=%v run_id=%d", err, resp.RunID)
	}
	var status, source string
	var alts int
	if err := pool.QueryRow(ctx, `SELECT status, source, jsonb_array_length(alternatives) FROM segmentation_runs WHERE id=$1`, resp.RunID).
		Scan(&status, &source, &alts); err != nil {
		t.Fatal(err)
	}
	if status != "ok" || source != "mock" || alts == 0 {
		t.Fatalf("got status=%s source=%s alts=%d", status, source, alts)
	}

	if _, err := NewService(pool, refusingGen{}, log).Suggest(ctx, "something refused"); err == nil {
		t.Fatal("expected refusal error")
	}
	var raw string
	var in int
	if err := pool.QueryRow(ctx, `SELECT status, raw_output, input_tokens FROM segmentation_runs WHERE status='refusal'`).Scan(&status, &raw, &in); err != nil {
		t.Fatal(err)
	}
	if raw != "{}" || in != 10 {
		t.Fatalf("refusal trace incomplete: raw=%q in=%d", raw, in)
	}
}

func TestRunReaders(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := NewService(pool, refusingGen{}, slog.New(slog.DiscardHandler))
	// refusal path needs a catalog query, which works on empty tables
	_, _ = s.Suggest(ctx, "something refused")
	list, err := s.ListRuns(ctx, "refusal", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	d, err := s.GetRun(ctx, list[0].ID)
	if err != nil || len(d.Trace) != 4 || d.Trace[0].Status != "failed" || d.Trace[1].Status != "skipped" || d.Trace[2].Status != "skipped" {
		t.Fatalf("detail: %v %+v", err, d)
	}
	if _, ok := d.Trace[0].Detail.(map[string]any)["raw_output"]; !ok {
		t.Fatal("detail should include raw_output")
	}
	if _, ok := list[0].Trace[0].Detail.(map[string]any)["raw_output"]; ok {
		t.Fatal("list should omit raw_output")
	}
	if _, err := s.GetRun(ctx, 999999); !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}
