package customers

import (
	"context"
	"fmt"
	"time"
)

const day = 24 * time.Hour

// Audience definitions. The seed generator's archetypes are tuned to these.
const (
	abandonedWindow = 30 * day
	winBackAfter    = 90 * day
	vipMinOrders    = 4
	vipMinSpend     = 5000.0 // TRY, lifetime
	categoryWindow  = 180 * day
	newWindow       = 30 * day
	inactiveAfter   = 60 * day
)

// params collects bind values and hands out their $n placeholders, so audience
// predicates never contain user input as SQL text.
type params struct{ args []any }

func (p *params) add(v any) string {
	p.args = append(p.args, v)
	return fmt.Sprintf("$%d", len(p.args))
}

// segment is a named audience: a predicate over the per-customer stats row "s".
type segment struct {
	id            string
	name          string
	description   func(category string) string
	needsCategory bool
	where         func(p *params, now time.Time, category string) string
}

var segments = []segment{
	{
		id: "abandoned_carts", name: "Abandoned carts",
		description: func(string) string {
			return fmt.Sprintf("Added to cart in the last %d days, no purchase since", abandonedWindow/day)
		},
		where: func(p *params, now time.Time, _ string) string {
			return fmt.Sprintf("(s.last_cart_at > %s AND (s.last_order_at IS NULL OR s.last_cart_at > s.last_order_at))",
				p.add(now.Add(-abandonedWindow)))
		},
	},
	{
		id: "win_back", name: "Win-back",
		description: func(string) string {
			return fmt.Sprintf("Bought before, but not in the last %d days", winBackAfter/day)
		},
		where: func(p *params, now time.Time, _ string) string {
			return fmt.Sprintf("(s.order_count > 0 AND s.last_order_at < %s)", p.add(now.Add(-winBackAfter)))
		},
	},
	{
		id: "vip", name: "VIP",
		description: func(string) string {
			return fmt.Sprintf("%d+ orders and %.0f+ TRY lifetime spend", vipMinOrders, vipMinSpend)
		},
		where: func(p *params, _ time.Time, _ string) string {
			return fmt.Sprintf("(s.order_count >= %s AND s.total_spent >= %s)", p.add(vipMinOrders), p.add(vipMinSpend))
		},
	},
	{
		id: "category_buyers", name: "Category buyers", needsCategory: true,
		description: func(category string) string {
			return fmt.Sprintf("Bought %s in the last %d days", category, categoryWindow/day)
		},
		where: func(p *params, now time.Time, category string) string {
			return fmt.Sprintf("EXISTS (SELECT 1 FROM orders o WHERE o.customer_id = s.id AND o.category = %s AND o.placed_at > %s)",
				p.add(category), p.add(now.Add(-categoryWindow)))
		},
	},
	{
		id: "new_customers", name: "New customers",
		description: func(string) string {
			return fmt.Sprintf("Joined in the last %d days, no purchase yet", newWindow/day)
		},
		where: func(p *params, now time.Time, _ string) string {
			return fmt.Sprintf("(s.created_at > %s AND s.order_count = 0)", p.add(now.Add(-newWindow)))
		},
	},
	{
		id: "inactive_visitors", name: "Inactive visitors",
		description: func(string) string {
			return fmt.Sprintf("Never purchased, not active in the last %d days", inactiveAfter/day)
		},
		where: func(p *params, now time.Time, _ string) string {
			return fmt.Sprintf("(s.order_count = 0 AND s.last_active_at < %s)", p.add(now.Add(-inactiveAfter)))
		},
	},
}

// ValidateSegment checks a segment id (and its category, where required).
// An empty id means "no audience filter".
func ValidateSegment(id, category string) error {
	if id == "" {
		return nil
	}
	for _, sg := range segments {
		if sg.id == id {
			if sg.needsCategory && category == "" {
				return fmt.Errorf("category is required for segment %q", id)
			}
			return nil
		}
	}
	return fmt.Errorf("unknown segment %q", id)
}

func segmentByID(id string) (segment, bool) {
	for _, sg := range segments {
		if sg.id == id {
			return sg, true
		}
	}
	return segment{}, false
}

// Audience is one segment with its size and how many of its members can be
// reached on each channel (consent is evaluated per channel).
type Audience struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Size          int    `json:"size"`
	EmailEligible int    `json:"email_eligible"`
	SMSEligible   int    `json:"sms_eligible"`
}

type AudienceReport struct {
	// Category is the one used by the category-buyers audience.
	Category   string     `json:"category"`
	Categories []string   `json:"categories"`
	Audiences  []Audience `json:"audiences"`
}

// Audiences counts every audience in one pass over the customer stats.
// An empty category defaults to the best-selling one.
func (s *Store) Audiences(ctx context.Context, now time.Time, category string) (AudienceReport, error) {
	rep := AudienceReport{Categories: []string{}, Audiences: []Audience{}}

	rows, err := s.pool.Query(ctx, "SELECT category FROM orders GROUP BY category ORDER BY count(*) DESC, category")
	if err != nil {
		return rep, err
	}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return rep, err
		}
		rep.Categories = append(rep.Categories, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return rep, err
	}
	if category == "" && len(rep.Categories) > 0 {
		category = rep.Categories[0]
	}
	rep.Category = category

	var p params
	query := "SELECT "
	for i, sg := range segments {
		pred := sg.where(&p, now, category) // built once, reused for the three counts
		if i > 0 {
			query += ", "
		}
		query += fmt.Sprintf("count(*) FILTER (WHERE %[1]s), count(*) FILTER (WHERE %[1]s AND s.email_consent), count(*) FILTER (WHERE %[1]s AND s.sms_consent)", pred)
	}
	query += " FROM s"

	counts := make([]int, 3*len(segments))
	dest := make([]any, len(counts))
	for i := range counts {
		dest[i] = &counts[i]
	}
	if err := s.pool.QueryRow(ctx, statsCTE+query, p.args...).Scan(dest...); err != nil {
		return rep, err
	}

	for i, sg := range segments {
		rep.Audiences = append(rep.Audiences, Audience{
			ID: sg.id, Name: sg.name, Description: sg.description(category),
			Size: counts[3*i], EmailEligible: counts[3*i+1], SMSEligible: counts[3*i+2],
		})
	}
	return rep, nil
}
