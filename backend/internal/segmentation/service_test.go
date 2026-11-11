package segmentation

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/smart-campaign-composer/backend/internal/db"
	"github.com/smart-campaign-composer/backend/internal/dbtest"
)

// scripted returns a fixed reply, standing in for Claude.
type scripted GenResult

func (scripted) Source() string { return "claude" }
func (g scripted) Generate(context.Context, string, Catalog, time.Time) (GenResult, error) {
	return GenResult(g), nil
}

func TestSuggestOutcomes(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO customers (first_name, last_name, email, phone, city, email_consent, sms_consent) VALUES
		('A','A','a@example.com','+905320000001','Istanbul', true, true),
		('B','B','b@example.com',NULL,'Ankara', true, false);
		INSERT INTO orders (customer_id, category, total_amount, placed_at) SELECT id, 'fashion', 10, now() FROM customers`); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	istanbul := Condition{Field: "city", Op: "in", Strings: []string{"Istanbul"}}
	good := Alternative{Name: "Istanbul", Reasoning: "Exactly the brief.", Match: "all", Conditions: []Condition{istanbul}}
	sup := Assessment{Outcome: OutcomeSupported, Message: "ok"}

	t.Run("unsupported never executes alternatives", func(t *testing.T) {
		g := scripted{Assessment: Assessment{Outcome: OutcomeUnsupported, Message: "No subscription data.", Unsupported: []string{"subscription cancellation"}},
			Alternatives: []Alternative{good}}
		resp, err := NewService(pool, g, log).Suggest(ctx, "cancelled subscriptions")
		if err != nil || resp.Outcome != OutcomeUnsupported || len(resp.Alternatives) != 0 || len(resp.Unsupported) != 1 {
			t.Fatalf("got %+v, %v", resp, err)
		}
		var status string
		_ = pool.QueryRow(ctx, `SELECT status FROM segmentation_runs WHERE id=$1`, resp.RunID).Scan(&status)
		if status != "unsupported" {
			t.Fatalf("trace status %q", status)
		}
	})

	t.Run("clarification", func(t *testing.T) {
		g := scripted{Assessment: Assessment{Outcome: OutcomeClarification, Message: "What is VIP?", Questions: []string{"Minimum spend?"}}}
		resp, _ := NewService(pool, g, log).Suggest(ctx, "vip customers")
		if resp.Outcome != OutcomeClarification || len(resp.Questions) != 1 || len(resp.Alternatives) != 0 {
			t.Fatalf("got %+v", resp)
		}
	})

	t.Run("rejected primary is not replaced by a variant", func(t *testing.T) {
		broken := Alternative{Name: "Faithful", Reasoning: "Exactly the brief.", Match: "all", Conditions: []Condition{{Field: "total_spent", Op: "gte"}}}
		resp, _ := NewService(pool, scripted{Assessment: sup, Alternatives: []Alternative{broken, good}}, log).Suggest(ctx, "x brief")
		if resp.Outcome != OutcomeSupported || len(resp.Alternatives) != 1 || resp.Alternatives[0].Primary {
			t.Fatalf("got %+v", resp)
		}
		if len(resp.Warnings) < 2 || strings.Contains(strings.Join(resp.Warnings, " "), "needs 1") {
			t.Fatalf("warnings %v", resp.Warnings)
		}
	})

	t.Run("nothing valid is invalid, not an error", func(t *testing.T) {
		contradict := Alternative{Name: "Contradiction", Reasoning: "Never ordered, ordered.", Match: "all", Conditions: []Condition{
			{Field: "order_count", Op: "eq", Numbers: []float64{0}}, {Field: "days_since_last_order", Op: "lte", Numbers: []float64{7}}}}
		resp, err := NewService(pool, scripted{Assessment: sup, Alternatives: []Alternative{contradict}}, log).Suggest(ctx, "x brief")
		if err != nil || resp.Outcome != OutcomeInvalid || !strings.Contains(resp.Warnings[0], "contradict") {
			t.Fatalf("got %+v, %v", resp, err)
		}
	})

	t.Run("valid empty audience", func(t *testing.T) {
		many := Alternative{Name: "Heavy buyers", Reasoning: "500+ orders in Istanbul.", Match: "all", Conditions: []Condition{istanbul, {Field: "order_count", Op: "gte", Numbers: []float64{500}}}}
		resp, _ := NewService(pool, scripted{Assessment: sup, Alternatives: []Alternative{many}}, log).Suggest(ctx, "x brief")
		if resp.Outcome != OutcomeSupported || len(resp.Alternatives) != 1 || !resp.Alternatives[0].Empty || !resp.Alternatives[0].Primary {
			t.Fatalf("got %+v", resp)
		}
	})

	t.Run("opted-out channel is never eligible", func(t *testing.T) {
		optedOut := Alternative{Name: "SMS opt-outs", Reasoning: "Customers without SMS consent.", Match: "all", Conditions: []Condition{{Field: "sms_consent", Op: "is_false"}}}
		resp, _ := NewService(pool, scripted{Assessment: sup, Alternatives: []Alternative{optedOut}}, log).Suggest(ctx, "x brief")
		a := resp.Alternatives[0]
		if a.Counts.Total != 1 || a.Counts.SMSEligible != 0 || len(resp.Warnings) != 1 {
			t.Fatalf("got %+v warnings %v", a.Counts, resp.Warnings)
		}
	})
}
