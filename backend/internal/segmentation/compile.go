package segmentation

import (
	"fmt"
	"strings"
	"time"
)

const day = 24 * time.Hour

type binds struct{ args []any }

func (b *binds) add(v any) string {
	b.args = append(b.args, v)
	return fmt.Sprintf("$%d", len(b.args))
}

// Compile turns a validated alternative into a SQL predicate over the
// customer stats row "s" plus its bind arguments. Call Validate first.
func Compile(a Alternative, now time.Time) (string, []any) {
	var b binds
	return compileInto(&b, a, now), b.args
}

func compileInto(b *binds, a Alternative, now time.Time) string {
	parts := make([]string, 0, len(a.Conditions))
	for _, c := range a.Conditions {
		parts = append(parts, compileCondition(b, c, now))
	}
	join := " AND "
	if a.Match == "any" {
		join = " OR "
	}
	return "(" + strings.Join(parts, join) + ")"
}

// CompileGroups compiles an audience plus ordered group rules (all validated)
// into one predicate and a group expression: the first matching group wins
// (1..n) and members matching none get 0, the remainder group. Groups are
// therefore mutually exclusive and cover the whole audience.
func CompileGroups(audience Alternative, groups []Alternative, now time.Time) (where, groupExpr string, args []any) {
	var b binds
	where = compileInto(&b, audience, now)
	if len(groups) == 0 {
		return where, "0", b.args
	}
	var g strings.Builder
	g.WriteString("CASE")
	for i, grp := range groups {
		fmt.Fprintf(&g, " WHEN %s THEN %d", compileInto(&b, grp, now), i+1)
	}
	g.WriteString(" ELSE 0 END")
	return where, g.String(), b.args
}

func compileCondition(b *binds, c Condition, now time.Time) string {
	spec := registry[c.Field]
	ago := func(d float64) string { return b.add(now.Add(-time.Duration(d * float64(day)))) }

	switch c.Field {
	case "city", "language":
		if c.Op == "in" {
			return fmt.Sprintf("s.%s = ANY(%s)", spec.column, b.add(c.Strings))
		}
		return fmt.Sprintf("s.%s <> ALL(%s)", spec.column, b.add(c.Strings))
	case "email_consent", "sms_consent":
		if c.Op == "is_true" {
			return "s." + spec.column
		}
		return "NOT s." + spec.column
	case "cart_after_last_order":
		pending := "(s.last_cart_at IS NOT NULL AND (s.last_order_at IS NULL OR s.last_cart_at > s.last_order_at))"
		if c.Op == "is_true" {
			return pending
		}
		return "NOT " + pending
	case "bought_category", "carted_category":
		col := "o.placed_at"
		if spec.table == "cart_additions" {
			col = "o.added_at"
		}
		q := fmt.Sprintf("EXISTS (SELECT 1 FROM %s o WHERE o.customer_id = s.id AND o.category = %s", spec.table, b.add(c.Category))
		if c.WithinDays > 0 {
			q += fmt.Sprintf(" AND %s > %s", col, ago(float64(c.WithinDays)))
		}
		q += ")"
		if c.Op == "not_exists" {
			return "NOT " + q
		}
		return q
	case "order_count", "total_spent":
		col := "s." + spec.column
		switch c.Op {
		case "eq":
			return fmt.Sprintf("%s = %s", col, b.add(c.Numbers[0]))
		case "gte":
			return fmt.Sprintf("%s >= %s", col, b.add(c.Numbers[0]))
		case "lte":
			return fmt.Sprintf("%s <= %s", col, b.add(c.Numbers[0]))
		default:
			return fmt.Sprintf("%s BETWEEN %s AND %s", col, b.add(c.Numbers[0]), b.add(c.Numbers[1]))
		}
	default: // days_since_*: compare the timestamp against now - N days
		col := "s." + spec.column
		switch c.Op {
		case "lte": // happened within the last N days
			return fmt.Sprintf("%s >= %s", col, ago(c.Numbers[0]))
		case "gte": // happened more than N days ago
			return fmt.Sprintf("%s <= %s", col, ago(c.Numbers[0]))
		case "between":
			return fmt.Sprintf("%s BETWEEN %s AND %s", col, ago(c.Numbers[1]), ago(c.Numbers[0]))
		case "is_null":
			return col + " IS NULL"
		default:
			return col + " IS NOT NULL"
		}
	}
}
