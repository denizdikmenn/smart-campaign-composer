package customers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/smart-campaign-composer/backend/internal/db"
	"github.com/smart-campaign-composer/backend/internal/dbtest"
	"github.com/smart-campaign-composer/backend/internal/seed"
)

var now = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

func ptr(b bool) *bool { return &b }

// TestFilters exercises list filters and pagination on a tiny hand-made table.
func TestFilters(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO customers (first_name, last_name, email, phone, city, email_consent, sms_consent) VALUES
		('A', 'A', 'a@example.com', '+905320000001', 'Istanbul', true,  true),
		('B', 'B', 'b@example.com', NULL,            'Istanbul', true,  false),
		('C', 'C', 'c@example.com', '+905320000003', 'Ankara',   false, true),
		('D', 'D', 'd@example.com', NULL,            'Ankara',   false, false),
		('E', 'E', 'e@example.com', NULL,            'Izmir',    true,  false)`)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(pool)

	sum, err := s.Summary(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 5 || sum.EmailConsent != 3 || sum.SMSConsent != 2 || sum.Buyers != 0 || sum.AvgOrderValue != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	if got := sum.Cities; len(got) != 3 || got[0] != "Ankara" || got[1] != "Istanbul" || got[2] != "Izmir" {
		t.Fatalf("cities = %v", got)
	}

	tests := []struct {
		name      string
		f         Filter
		wantTotal int
	}{
		{"no filter", Filter{}, 5},
		{"city", Filter{City: "Istanbul"}, 2},
		{"email consented", Filter{EmailConsent: ptr(true)}, 3},
		{"not sms consented", Filter{SMSConsent: ptr(false)}, 3},
		{"city + email + sms", Filter{City: "Istanbul", EmailConsent: ptr(true), SMSConsent: ptr(true)}, 1},
		{"no match", Filter{City: "Nowhere"}, 0},
		// Hostile input is only ever a bound value, never SQL.
		{"injection attempt", Filter{City: "Ankara' OR '1'='1"}, 0},
		{"injection via category", Filter{Segment: "category_buyers", Category: "x'; DROP TABLE customers;--"}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			items, total, err := s.List(ctx, tc.f, time.Now(), 100, 0)
			if err != nil {
				t.Fatal(err)
			}
			if total != tc.wantTotal || len(items) != tc.wantTotal || items == nil {
				t.Fatalf("total=%d items=%d (nil=%v), want %d", total, len(items), items == nil, tc.wantTotal)
			}
		})
	}

	t.Run("pagination", func(t *testing.T) {
		items, total, err := s.List(ctx, Filter{}, time.Now(), 2, 2)
		if err != nil || total != 5 || len(items) != 2 || items[0].FirstName != "C" {
			t.Fatalf("items=%+v total=%d err=%v", items, total, err)
		}
		items, total, err = s.List(ctx, Filter{}, time.Now(), 2, 10) // past the end
		if err != nil || total != 5 || len(items) != 0 {
			t.Fatalf("past end: items=%d total=%d err=%v", len(items), total, err)
		}
	})

	// Orders for C (2) and A (1); B has a cart addition only.
	_, err = pool.Exec(ctx, `
		INSERT INTO orders (customer_id, category, total_amount, placed_at)
		SELECT id, 'books', 100, now() - interval '3 days' FROM customers WHERE email IN ('a@example.com', 'c@example.com');
		INSERT INTO orders (customer_id, category, total_amount, placed_at)
		SELECT id, 'toys', 250, now() - interval '1 day' FROM customers WHERE email = 'c@example.com';
		INSERT INTO cart_additions (customer_id, category, added_at)
		SELECT id, 'shoes', now() FROM customers WHERE email = 'b@example.com'`)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("sort", func(t *testing.T) {
		first := func(srt Sort) string {
			items, _, err := s.List(ctx, Filter{Sort: srt}, time.Now(), 100, 0)
			if err != nil || len(items) != 5 {
				t.Fatalf("%+v: items=%d err=%v", srt, len(items), err)
			}
			return items[0].FirstName
		}
		if got := first(Sort{Field: "total_spent", Desc: true}); got != "C" {
			t.Errorf("total_spent desc: first = %s, want C", got)
		}
		if got := first(Sort{Field: "name", Desc: true}); got != "E" {
			t.Errorf("name desc: first = %s, want E", got)
		}
		// NULL last_order_at sorts last in both directions.
		if got := first(Sort{Field: "last_order_at"}); got != "A" && got != "C" {
			t.Errorf("last_order_at asc: first = %s, want a buyer", got)
		}
		if got := first(Sort{Field: "last_order_at", Desc: true}); got != "C" {
			t.Errorf("last_order_at desc: first = %s, want C", got)
		}
	})

	t.Run("get", func(t *testing.T) {
		var cID, bID int64
		if err := pool.QueryRow(ctx, "SELECT id FROM customers WHERE email = 'c@example.com'").Scan(&cID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, "SELECT id FROM customers WHERE email = 'b@example.com'").Scan(&bID); err != nil {
			t.Fatal(err)
		}
		d, err := s.Get(ctx, cID)
		if err != nil || d.Orders != 2 || d.TotalSpent != 350 || len(d.RecentOrders) != 2 || d.RecentOrders[0].Category != "toys" || d.Carts != 0 {
			t.Fatalf("C: %+v err=%v", d, err)
		}
		d, err = s.Get(ctx, bID)
		if err != nil || d.Carts != 1 || len(d.RecentCarts) != 1 || d.LastCartAt == nil || d.RecentOrders == nil {
			t.Fatalf("B: %+v err=%v", d, err)
		}
		if _, err := s.Get(ctx, 999999); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown id: err = %v", err)
		}
	})
}

func TestParseSort(t *testing.T) {
	if s, err := ParseSort("", ""); err != nil || s != (Sort{}) || s.orderBy() != " ORDER BY s.id ASC" {
		t.Fatalf("default: %+v %q %v", s, s.orderBy(), err)
	}
	s, err := ParseSort("name", "desc")
	if err != nil || s.orderBy() != " ORDER BY lower(s.last_name) DESC NULLS LAST, lower(s.first_name) DESC NULLS LAST, s.id" {
		t.Fatalf("name desc: %q %v", s.orderBy(), err)
	}
	for _, bad := range [][2]string{{"email", ""}, {"s.id; DROP TABLE customers", ""}, {"name", "sideways"}} {
		if _, err := ParseSort(bad[0], bad[1]); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

// reference computes the audiences straight from the generated dataset, as an
// independent check of the SQL definitions.
func reference(ds seed.Dataset, category string) map[string]map[int64]bool {
	type stat struct {
		orders              int
		spent               int64
		lastOrder, lastCart time.Time
		boughtCategory      bool
	}
	st := map[int64]*stat{}
	get := func(id int64) *stat {
		if st[id] == nil {
			st[id] = &stat{}
		}
		return st[id]
	}
	for _, o := range ds.Orders {
		s := get(o.CustomerID)
		s.orders++
		s.spent += o.TotalCents
		if o.PlacedAt.After(s.lastOrder) {
			s.lastOrder = o.PlacedAt
		}
		if o.Category == category && o.PlacedAt.After(now.Add(-categoryWindow)) {
			s.boughtCategory = true
		}
	}
	for _, c := range ds.CartAdditions {
		if s := get(c.CustomerID); c.AddedAt.After(s.lastCart) {
			s.lastCart = c.AddedAt
		}
	}

	out := map[string]map[int64]bool{}
	for _, sg := range segments {
		out[sg.id] = map[int64]bool{}
	}
	for _, c := range ds.Customers {
		s := get(c.ID)
		in := map[string]bool{
			"abandoned_carts":   s.lastCart.After(now.Add(-abandonedWindow)) && s.lastCart.After(s.lastOrder),
			"win_back":          s.orders > 0 && s.lastOrder.Before(now.Add(-winBackAfter)),
			"vip":               s.orders >= vipMinOrders && float64(s.spent)/100 >= vipMinSpend,
			"category_buyers":   s.boughtCategory,
			"new_customers":     c.CreatedAt.After(now.Add(-newWindow)) && s.orders == 0,
			"inactive_visitors": s.orders == 0 && c.LastActiveAt.Before(now.Add(-inactiveAfter)),
		}
		for id, member := range in {
			if member {
				out[id][c.ID] = true
			}
		}
	}
	return out
}

// TestAudiencesMatchSeededData seeds the full dataset and checks every audience
// (size, per-channel eligibility and list totals) against the reference above.
func TestAudiencesMatchSeededData(t *testing.T) {
	const n = 5000
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if seeded, err := seed.SeedIfEmpty(ctx, pool, n, now); err != nil || !seeded {
		t.Fatalf("seed: %v %v", seeded, err)
	}
	ds := seed.Generate(n, now)
	byID := map[int64]seed.Customer{}
	for _, c := range ds.Customers {
		byID[c.ID] = c
	}
	s := NewStore(pool)

	rep, err := s.Audiences(ctx, now, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Audiences) != len(segments) || rep.Category == "" || len(rep.Categories) == 0 {
		t.Fatalf("report = %+v", rep)
	}
	want := reference(ds, rep.Category)

	for _, a := range rep.Audiences {
		members := want[a.ID]
		var email, sms int
		for id := range members {
			if byID[id].EmailConsent {
				email++
			}
			if byID[id].SMSConsent {
				sms++
			}
		}
		if a.Size != len(members) || a.EmailEligible != email || a.SMSEligible != sms {
			t.Errorf("%s: got size=%d email=%d sms=%d, want size=%d email=%d sms=%d",
				a.ID, a.Size, a.EmailEligible, a.SMSEligible, len(members), email, sms)
		}
		t.Logf("%-18s size=%4d (%4.1f%%) email=%4d sms=%4d", a.ID, a.Size, 100*float64(a.Size)/n, a.EmailEligible, a.SMSEligible)
		// Every audience should be non-trivial but not everyone.
		if f := float64(a.Size) / n; f < 0.03 || f > 0.30 {
			t.Errorf("%s: share = %.3f, want in [0.03, 0.30]", a.ID, f)
		}
		if a.EmailEligible > a.Size || a.SMSEligible > a.Size || a.Description == "" {
			t.Errorf("%s: inconsistent %+v", a.ID, a)
		}

		// The list endpoint filters by exactly the same definition.
		f := Filter{Segment: a.ID, Category: rep.Category}
		items, total, err := s.List(ctx, f, now, 50, 0)
		if err != nil || total != a.Size {
			t.Errorf("%s: list total=%d err=%v, want %d", a.ID, total, err, a.Size)
		}
		for _, c := range items {
			if !members[c.ID] {
				t.Errorf("%s: customer %d listed but not in reference audience", a.ID, c.ID)
			}
		}
		// Channel eligibility composes with the audience.
		if _, total, err := s.List(ctx, Filter{Segment: a.ID, Category: rep.Category, SMSConsent: ptr(true)}, now, 1, 0); err != nil || total != a.SMSEligible {
			t.Errorf("%s: sms-consented total=%d err=%v, want %d", a.ID, total, err, a.SMSEligible)
		}
	}

	// A different category changes only the category audience.
	other := rep.Categories[len(rep.Categories)-1]
	rep2, err := s.Audiences(ctx, now, other)
	if err != nil || rep2.Category != other {
		t.Fatalf("category %q: %+v %v", other, rep2, err)
	}
	for i, a := range rep2.Audiences {
		changed := a.Size != rep.Audiences[i].Size
		if changed != (a.ID == "category_buyers") {
			t.Errorf("%s: size changed=%v with category", a.ID, changed)
		}
	}

	// Behavioural summary.
	sum, err := s.Summary(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	buyers, repeat := map[int64]int{}, 0
	for _, o := range ds.Orders {
		buyers[o.CustomerID]++
	}
	for _, c := range buyers {
		if c > 1 {
			repeat++
		}
	}
	if sum.Total != n || sum.Buyers != len(buyers) || sum.RepeatBuyers != repeat || sum.AvgOrderValue <= 0 || sum.Active30d == 0 {
		t.Errorf("summary = %+v (buyers %d, repeat %d)", sum, len(buyers), repeat)
	}
}
