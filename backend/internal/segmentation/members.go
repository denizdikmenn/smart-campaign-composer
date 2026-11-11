package segmentation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smart-campaign-composer/backend/internal/customers"
)

// Member is one audience member with the attributes used for grouping,
// previews and personalization. Contact details are deliberately absent.
type Member struct {
	ID           int64      `json:"id"`
	FirstName    string     `json:"first_name"`
	Language     string     `json:"language"`
	City         string     `json:"city"`
	EmailConsent bool       `json:"email_consent"`
	SMSConsent   bool       `json:"sms_consent"`
	OrderCount   int        `json:"order_count"`
	TotalSpent   float64    `json:"total_spent"`
	LastOrderAt  *time.Time `json:"last_order_at"`
	LastCartAt   *time.Time `json:"last_cart_at"`
	LastActiveAt time.Time  `json:"last_active_at"`
	CreatedAt    time.Time  `json:"created_at"`
	// Group is the behavioral group index from Members (0 = remainder).
	Group int `json:"group"`
}

// Reachable reports whether the member consented to at least one channel.
func (m Member) Reachable() bool { return m.EmailConsent || m.SMSConsent }

// Catalog returns the values rules may refer to (cities, languages, categories).
func (s *Service) Catalog(ctx context.Context) (Catalog, error) {
	cat, _, err := s.catalog(ctx)
	return cat, err
}

// ValidateRules normalizes and validates rules against the current catalog.
// Only the rules are checked; name and reasoning are kept as given.
func (s *Service) ValidateRules(ctx context.Context, a Alternative) (Alternative, error) {
	cat, _, err := s.catalog(ctx)
	if err != nil {
		return a, err
	}
	name, reasoning := a.Name, a.Reasoning
	a.Name, a.Reasoning = "rules", "Server-side validation of rules."
	a, _ = Normalize(a)
	if err := Validate(a, cat); err != nil {
		return a, fmt.Errorf("%w: %w", ErrInvalidRules, err)
	}
	a.Name, a.Reasoning = name, reasoning
	return a, nil
}

// Members calls fn for every member of the audience (ordered by id), with
// Group set by the first matching group rule (1..n) or 0. The audience and
// group rules are validated again here.
func (s *Service) Members(ctx context.Context, audience Alternative, groups []Alternative, fn func(Member) error) error {
	audience, err := s.ValidateRules(ctx, audience)
	if err != nil {
		return err
	}
	for i := range groups {
		if groups[i], err = s.ValidateRules(ctx, groups[i]); err != nil {
			return fmt.Errorf("group %d: %w", i+1, err)
		}
	}
	where, groupExpr, args := CompileGroups(audience, groups, s.now())
	ctx, cancel := context.WithTimeout(ctx, 2*queryTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, customers.StatsCTE+`SELECT id, first_name, language, city, email_consent, sms_consent,
		order_count, total_spent, last_order_at, last_cart_at, last_active_at, created_at, `+groupExpr+`
		FROM s WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.FirstName, &m.Language, &m.City, &m.EmailConsent, &m.SMSConsent,
			&m.OrderCount, &m.TotalSpent, &m.LastOrderAt, &m.LastCartAt, &m.LastActiveAt, &m.CreatedAt, &m.Group); err != nil {
			return err
		}
		if err := fn(m); err != nil {
			return err
		}
	}
	return rows.Err()
}

// OrderEvent and CartEvent are a customer's recent activity.
type OrderEvent struct {
	Category string    `json:"category"`
	Amount   float64   `json:"amount"`
	At       time.Time `json:"at"`
}

type CartEvent struct {
	Category string    `json:"category"`
	At       time.Time `json:"at"`
}

// History is the activity available for personalizing one customer.
type History struct {
	Member
	RecentOrders []OrderEvent `json:"recent_orders"` // newest first, at most 5
	RecentCarts  []CartEvent  `json:"recent_carts"`  // newest first, at most 5, last 180 days
}

// ErrNotMember is returned when a customer is not in the audience.
var ErrNotMember = errors.New("customer is not in this audience")

// CustomerHistory returns the member (verified to be in the audience) and
// their recent orders and cart additions.
func (s *Service) CustomerHistory(ctx context.Context, audience Alternative, customerID int64) (History, error) {
	var h History
	found := false
	err := s.Members(ctx, audience, nil, func(m Member) error {
		if m.ID == customerID {
			h.Member, found = m, true
		}
		return nil
	})
	if err != nil {
		return h, err
	}
	if !found {
		return h, ErrNotMember
	}
	return s.RecentActivity(ctx, h.Member)
}

// RecentActivity loads a member's recent orders and cart additions. The
// caller is responsible for the member coming from the audience (Members).
func (s *Service) RecentActivity(ctx context.Context, m Member) (History, error) {
	h := History{Member: m}
	customerID := m.ID
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	h.RecentOrders, h.RecentCarts = []OrderEvent{}, []CartEvent{}
	rows, err := s.pool.Query(ctx, `SELECT category, total_amount::float8, placed_at FROM orders WHERE customer_id = $1 ORDER BY placed_at DESC LIMIT 5`, customerID)
	if err != nil {
		return h, err
	}
	for rows.Next() {
		var o OrderEvent
		if err := rows.Scan(&o.Category, &o.Amount, &o.At); err != nil {
			rows.Close()
			return h, err
		}
		h.RecentOrders = append(h.RecentOrders, o)
	}
	rows.Close()
	rows, err = s.pool.Query(ctx, `SELECT category, added_at FROM cart_additions
		WHERE customer_id = $1 AND added_at > $2 ORDER BY added_at DESC LIMIT 5`, customerID, s.now().Add(-180*day))
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		var c CartEvent
		if err := rows.Scan(&c.Category, &c.At); err != nil {
			return h, err
		}
		h.RecentCarts = append(h.RecentCarts, c)
	}
	return h, rows.Err()
}

// ConditionSchema is the strict JSON schema of one rule condition, shared by
// every structured-output prompt that proposes rules.
func ConditionSchema() map[string]any {
	strArr := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	numArr := map[string]any{"type": "array", "items": map[string]any{"type": "number"}}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"field", "op", "strings", "numbers", "category", "within_days"},
		"properties": map[string]any{
			"field":       map[string]any{"type": "string", "enum": fieldNames()},
			"op":          map[string]any{"type": "string", "enum": opNames()},
			"strings":     strArr,
			"numbers":     numArr,
			"category":    map[string]any{"type": "string"},
			"within_days": map[string]any{"type": "integer"},
		},
	}
}

// FieldGuide describes the supported fields, operators and values for a
// prompt that asks the model to write rule conditions.
func FieldGuide(cat Catalog) string {
	var b strings.Builder
	b.WriteString(`Condition value fields (always include all of them; use [] / "" / 0 when unused):
- strings: for city/language (in / not_in), exact values from the lists below
- numbers: one whole number for eq/gte/lte, two for between [from,to]; only total_spent may have decimals
- category + within_days: only for bought_category / carted_category (exists / not_exists); within_days 0 = any time
- days_since_*: "lte" N = within the last N days; "gte" N = more than N days ago; "is_null" = never; "not_null" = at least once
- boolean fields use is_true / is_false

Supported fields:
`)
	for _, f := range fieldNames() {
		ops := make([]string, 0, len(registry[f].ops))
		for op := range registry[f].ops {
			ops = append(ops, op)
		}
		sort.Strings(ops)
		fmt.Fprintf(&b, "- %s: %s. ops: %s\n", f, registry[f].desc, strings.Join(ops, ", "))
	}
	fmt.Fprintf(&b, "\nCities: %s\nLanguages: %s\nCategories: %s\n", strings.Join(cat.Cities, ", "), strings.Join(cat.Languages, ", "), strings.Join(cat.Categories, ", "))
	return b.String()
}
