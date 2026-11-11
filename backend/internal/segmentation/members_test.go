package segmentation

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/smart-campaign-composer/backend/internal/db"
	"github.com/smart-campaign-composer/backend/internal/dbtest"
)

func TestMembersGroups(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// 30 Istanbul customers; ids 1..10 have 3 orders, 11..20 have 1 order, 21..30 none.
	if _, err := pool.Exec(ctx, `
		INSERT INTO customers (id, first_name, last_name, email, city, language, email_consent)
		SELECT g, 'N' || g, 'L', 'c' || g || '@example.com', 'Istanbul', CASE WHEN g % 3 = 0 THEN 'en' ELSE 'tr' END, true FROM generate_series(1, 30) g;
		INSERT INTO orders (customer_id, category, total_amount, placed_at)
		SELECT c, 'fashion', 100, now() - interval '5 days' FROM generate_series(1, 10) c, generate_series(1, 3);
		INSERT INTO orders (customer_id, category, total_amount, placed_at)
		SELECT c, 'books', 50, now() - interval '200 days' FROM generate_series(11, 20) c;`); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool, Mock{}, slog.New(slog.DiscardHandler))
	audience := Alternative{Match: "all", Conditions: []Condition{{Field: "city", Op: "in", Strings: []string{"Istanbul"}}}}
	repeat := Alternative{Name: "Repeat", Match: "all", Conditions: []Condition{{Field: "order_count", Op: "gte", Numbers: []float64{2}}}}
	buyers := Alternative{Name: "Buyers", Match: "all", Conditions: []Condition{{Field: "order_count", Op: "gte", Numbers: []float64{1}}}}

	sizes := func(groups ...Alternative) map[int]int {
		out := map[int]int{}
		seen := map[int64]bool{}
		if err := svc.Members(ctx, audience, groups, func(m Member) error {
			if seen[m.ID] {
				t.Fatalf("customer %d listed twice", m.ID)
			}
			seen[m.ID] = true
			out[m.Group]++
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	// "Buyers" overlaps "Repeat": first match wins, the remainder gets the rest.
	got := sizes(repeat, buyers)
	if got[1] != 10 || got[2] != 10 || got[0] != 10 {
		t.Fatalf("sizes %v", got)
	}
	// Order matters, and nobody is lost: Repeat is now empty.
	got = sizes(buyers, repeat)
	if got[1] != 20 || got[2] != 0 || got[0] != 10 {
		t.Fatalf("reordered sizes %v", got)
	}
	for i := 0; i < 3; i++ { // deterministic
		if again := sizes(repeat, buyers); again[1] != 10 || again[0] != 10 {
			t.Fatalf("run %d: %v", i, again)
		}
	}
	bad := Alternative{Match: "all", Conditions: []Condition{{Field: "favourite_colour", Op: "in", Strings: []string{"red"}}}}
	if err := svc.Members(ctx, audience, []Alternative{bad}, func(Member) error { return nil }); !errors.Is(err, ErrInvalidRules) {
		t.Fatalf("invalid group: %v", err)
	}

	check, err := svc.CheckAudience(ctx, audience)
	if err != nil || check.Languages["en"] != 10 || check.Languages["tr"] != 20 {
		t.Fatalf("languages %v, %v", check.Languages, err)
	}
	h, err := svc.CustomerHistory(ctx, audience, 1)
	if err != nil || len(h.RecentOrders) != 3 || h.OrderCount != 3 {
		t.Fatalf("history %+v, %v", h, err)
	}
	if _, err := svc.CustomerHistory(ctx, audience, 999); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member: %v", err)
	}
}
