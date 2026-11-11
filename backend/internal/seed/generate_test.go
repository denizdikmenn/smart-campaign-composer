package seed

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)

func TestGenerateIsDeterministic(t *testing.T) {
	a, b := Generate(500, testNow), Generate(500, testNow)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same inputs produced different datasets")
	}
}

func TestGenerateInvariants(t *testing.T) {
	const n = 5000
	ds := Generate(n, testNow)
	if len(ds.Customers) != n {
		t.Fatalf("customers = %d, want %d", len(ds.Customers), n)
	}

	emails, phones := map[string]bool{}, map[string]bool{}
	created := map[int64]time.Time{}
	for _, c := range ds.Customers {
		created[c.ID] = c.CreatedAt
		if emails[c.Email] {
			t.Fatalf("duplicate email %s", c.Email)
		}
		emails[c.Email] = true
		if c.Email != strings.ToLower(c.Email) || strings.ContainsAny(c.Email, " ĞÜŞİÖÇığüşöç") {
			t.Fatalf("bad email %q", c.Email)
		}
		if !strings.HasPrefix(c.Email[strings.Index(c.Email, "@"):], "@example.") {
			t.Fatalf("email %q is not on a reserved domain", c.Email)
		}
		if c.Phone != nil {
			if phones[*c.Phone] {
				t.Fatalf("duplicate phone %s", *c.Phone)
			}
			phones[*c.Phone] = true
		}
		if c.SMSConsent && c.Phone == nil {
			t.Fatalf("customer %d has SMS consent without a phone", c.ID)
		}
		if !c.CreatedAt.Before(testNow) {
			t.Fatalf("customer %d created in the future", c.ID)
		}
	}
	latest := map[int64]time.Time{}
	note := func(id int64, at time.Time) {
		if at.After(latest[id]) {
			latest[id] = at
		}
	}
	for _, o := range ds.Orders {
		if o.TotalCents <= 0 || o.PlacedAt.Before(created[o.CustomerID]) || o.PlacedAt.After(testNow) {
			t.Fatalf("invalid order %+v", o)
		}
		note(o.CustomerID, o.PlacedAt)
	}
	for _, a := range ds.CartAdditions {
		if a.AddedAt.Before(created[a.CustomerID]) || a.AddedAt.After(testNow) {
			t.Fatalf("invalid cart addition %+v", a)
		}
		note(a.CustomerID, a.AddedAt)
	}
	for _, c := range ds.Customers {
		if c.LastActiveAt.Before(c.CreatedAt) || c.LastActiveAt.Before(latest[c.ID]) || c.LastActiveAt.After(testNow) {
			t.Fatalf("customer %d: last_active_at %v inconsistent (created %v, latest event %v)", c.ID, c.LastActiveAt, c.CreatedAt, latest[c.ID])
		}
		if c.Language != "tr" && c.Language != "en" && c.Language != "de" {
			t.Fatalf("customer %d: language %q", c.ID, c.Language)
		}
	}
}

// The distributions must be varied enough to make segmentation meaningful.
func TestGenerateDistributions(t *testing.T) {
	const n = 5000
	ds := Generate(n, testNow)
	frac := func(count int) float64 { return float64(count) / n }

	var istanbul, emailOnly, smsOnly, both, none int
	for _, c := range ds.Customers {
		if c.City == "Istanbul" {
			istanbul++
		}
		switch {
		case c.EmailConsent && c.SMSConsent:
			both++
		case c.EmailConsent:
			emailOnly++
		case c.SMSConsent:
			smsOnly++
		default:
			none++
		}
	}
	for name, c := range map[string]struct {
		got      int
		min, max float64
	}{
		"istanbul":   {istanbul, 0.22, 0.32},
		"both":       {both, 0.35, 0.50},
		"email only": {emailOnly, 0.28, 0.42},
		"sms only":   {smsOnly, 0.08, 0.18},
		"no consent": {none, 0.05, 0.14},
	} {
		if f := frac(c.got); f < c.min || f > c.max {
			t.Errorf("%s share = %.3f, want in [%.2f, %.2f]", name, f, c.min, c.max)
		}
	}

	ordersBy := map[int64]bool{}
	for _, o := range ds.Orders {
		ordersBy[o.CustomerID] = true
	}
	if f := frac(n - len(ordersBy)); f < 0.20 || f > 0.45 {
		t.Errorf("customers without orders = %.3f", f)
	}

	var english, german int
	for _, c := range ds.Customers {
		switch c.Language {
		case "en":
			english++
		case "de":
			german++
		}
	}
	if f := frac(english); f < 0.12 || f > 0.28 {
		t.Errorf("english share = %.3f", f)
	}
	if f := frac(german); f < 0.01 || f > 0.06 {
		t.Errorf("german share = %.3f", f)
	}
	if f := frac(n - english - german); f < 0.70 {
		t.Errorf("turkish share = %.3f, want at least 0.70", f)
	}
	// Names fit the language: Turkish names among foreign speakers are rare.
	turkish := map[string]bool{}
	for _, n := range append(append([]string{}, femaleNames...), maleNames...) {
		turkish[n] = true
	}
	mismatch := 0
	for _, c := range ds.Customers {
		if c.Language != "tr" && turkish[c.FirstName] {
			mismatch++
		}
		if c.Language == "tr" && !turkish[c.FirstName] {
			t.Fatalf("Turkish speaker with a foreign name: %+v", c)
		}
	}
	if mismatch > (english+german)/100 {
		t.Errorf("%d of %d foreign-language customers have Turkish names", mismatch, english+german)
	}
}
