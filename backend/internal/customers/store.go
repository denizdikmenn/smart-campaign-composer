// Package customers holds read-only customer and audience queries.
package customers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Summary struct {
	Total        int      `json:"total"`
	EmailConsent int      `json:"email_consent"`
	SMSConsent   int      `json:"sms_consent"`
	Cities       []string `json:"cities"`

	// Behavioural metrics.
	Buyers        int     `json:"buyers"`          // at least one order
	RepeatBuyers  int     `json:"repeat_buyers"`   // at least two orders
	Active30d     int     `json:"active_30d"`      // active in the last 30 days
	AvgOrderValue float64 `json:"avg_order_value"` // TRY
}

type Customer struct {
	ID           int64     `json:"id"`
	FirstName    string    `json:"first_name"`
	LastName     string    `json:"last_name"`
	Email        string    `json:"email"`
	Phone        *string   `json:"phone"`
	City         string    `json:"city"`
	Language     string    `json:"language"`
	EmailConsent bool      `json:"email_consent"`
	SMSConsent   bool      `json:"sms_consent"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`

	// Insights derived from orders.
	Orders      int        `json:"orders"`
	TotalSpent  float64    `json:"total_spent"` // TRY, lifetime
	LastOrderAt *time.Time `json:"last_order_at"`
}

// Filter narrows a customer list. Zero values mean "no filter".
type Filter struct {
	City         string
	EmailConsent *bool
	SMSConsent   *bool
	Segment      string // audience id, see ValidateSegment
	Category     string // used by the category_buyers audience
}

// statsCTE exposes one row per customer with order and cart aggregates ("s").
const statsCTE = `
WITH s AS (
	SELECT c.id, c.first_name, c.last_name, c.email, c.phone, c.city, c.language,
	       c.email_consent, c.sms_consent, c.created_at, c.last_active_at,
	       COALESCE(o.order_count, 0)        AS order_count,
	       COALESCE(o.total_spent, 0)::float8 AS total_spent,
	       o.last_order_at,
	       ca.last_cart_at
	FROM customers c
	LEFT JOIN (
		SELECT customer_id, count(*) AS order_count, sum(total_amount) AS total_spent, max(placed_at) AS last_order_at
		FROM orders GROUP BY customer_id
	) o ON o.customer_id = c.id
	LEFT JOIN (
		SELECT customer_id, max(added_at) AS last_cart_at
		FROM cart_additions GROUP BY customer_id
	) ca ON ca.customer_id = c.id
)
`

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Summary(ctx context.Context, now time.Time) (Summary, error) {
	var sum Summary
	err := s.pool.QueryRow(ctx, statsCTE+`
		SELECT count(*),
		       count(*) FILTER (WHERE email_consent),
		       count(*) FILTER (WHERE sms_consent),
		       count(*) FILTER (WHERE order_count > 0),
		       count(*) FILTER (WHERE order_count > 1),
		       count(*) FILTER (WHERE last_active_at > $1)
		FROM s`, now.Add(-30*day)).
		Scan(&sum.Total, &sum.EmailConsent, &sum.SMSConsent, &sum.Buyers, &sum.RepeatBuyers, &sum.Active30d)
	if err != nil {
		return sum, err
	}
	if err := s.pool.QueryRow(ctx, "SELECT COALESCE(avg(total_amount), 0)::float8 FROM orders").Scan(&sum.AvgOrderValue); err != nil {
		return sum, err
	}

	rows, err := s.pool.Query(ctx, "SELECT DISTINCT city FROM customers ORDER BY city")
	if err != nil {
		return sum, err
	}
	defer rows.Close()
	sum.Cities = []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return sum, err
		}
		sum.Cities = append(sum.Cities, c)
	}
	return sum, rows.Err()
}

// List returns one page of customers matching f, plus the total match count.
func (s *Store) List(ctx context.Context, f Filter, now time.Time, limit, offset int) ([]Customer, int, error) {
	var p params
	where := whereClause(&p, f, now)

	var total int
	if err := s.pool.QueryRow(ctx, statsCTE+"SELECT count(*) FROM s"+where, p.args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limitPh, offsetPh := p.add(limit), p.add(offset)
	rows, err := s.pool.Query(ctx, statsCTE+`
		SELECT id, first_name, last_name, email, phone, city, language, email_consent, sms_consent,
		       created_at, last_active_at, order_count, total_spent, last_order_at
		FROM s`+where+fmt.Sprintf(" ORDER BY id LIMIT %s OFFSET %s", limitPh, offsetPh), p.args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	items := []Customer{}
	for rows.Next() {
		var c Customer
		if err := rows.Scan(&c.ID, &c.FirstName, &c.LastName, &c.Email, &c.Phone, &c.City, &c.Language,
			&c.EmailConsent, &c.SMSConsent, &c.CreatedAt, &c.LastActiveAt, &c.Orders, &c.TotalSpent, &c.LastOrderAt); err != nil {
			return nil, 0, err
		}
		items = append(items, c)
	}
	return items, total, rows.Err()
}

// whereClause builds a WHERE clause using only placeholders; user input is
// never concatenated into the SQL text.
func whereClause(p *params, f Filter, now time.Time) string {
	var conds []string
	if f.City != "" {
		conds = append(conds, "s.city = "+p.add(f.City))
	}
	if f.EmailConsent != nil {
		conds = append(conds, "s.email_consent = "+p.add(*f.EmailConsent))
	}
	if f.SMSConsent != nil {
		conds = append(conds, "s.sms_consent = "+p.add(*f.SMSConsent))
	}
	if sg, ok := segmentByID(f.Segment); ok {
		conds = append(conds, sg.where(p, now, f.Category))
	}
	if len(conds) == 0 {
		return ""
	}
	return " WHERE " + strings.Join(conds, " AND ")
}
