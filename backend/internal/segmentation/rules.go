// Package segmentation turns structured audience rules (as proposed by an LLM)
// into validated, parameterized, read-only SQL and real audience counts.
// Rules are data, never SQL: every identifier comes from the registry below and
// every value reaches PostgreSQL as a bind parameter.
package segmentation

// Condition is one rule. Which value fields are used depends on Field/Op; the
// flat shape keeps the LLM output schema simple and strict.
type Condition struct {
	Field      string    `json:"field"`
	Op         string    `json:"op"`
	Strings    []string  `json:"strings"`     // city, language
	Numbers    []float64 `json:"numbers"`     // numeric and day-count values
	Category   string    `json:"category"`    // bought_category / carted_category
	WithinDays int       `json:"within_days"` // category window; 0 = any time
}

// Alternative is one proposed audience.
type Alternative struct {
	Name       string      `json:"name"`
	Reasoning  string      `json:"reasoning"`
	Match      string      `json:"match"` // "all" (AND) or "any" (OR)
	Conditions []Condition `json:"conditions"`
}

type valueKind int

const (
	kindNone     valueKind = iota // no value (is_true, is_null, ...)
	kindStrings                   // Strings
	kindNumber                    // Numbers[0]
	kindBetween                   // Numbers[0], Numbers[1]
	kindCategory                  // Category (+ WithinDays)
)

type opSpec struct {
	kind valueKind
	desc string
}

type fieldSpec struct {
	desc   string
	ops    map[string]opSpec
	column string // SQL column of the stats row "s"; empty for special fields
	table  string // orders / cart_additions for category fields
	min    float64
	max    float64
	// integer values only (counts and day numbers)
	integer bool
}

var (
	boolOps = map[string]opSpec{"is_true": {kindNone, "yes"}, "is_false": {kindNone, "no"}}
	numOps  = map[string]opSpec{
		"eq": {kindNumber, "equals"}, "gte": {kindNumber, "at least"},
		"lte": {kindNumber, "at most"}, "between": {kindBetween, "between"},
	}
	dayOps = map[string]opSpec{
		"lte": {kindNumber, "within the last N days"}, "gte": {kindNumber, "more than N days ago"},
		"between": {kindBetween, "between A and B days ago"},
		"is_null": {kindNone, "never"}, "not_null": {kindNone, "at least once"},
	}
	listOps = map[string]opSpec{"in": {kindStrings, "is one of"}, "not_in": {kindStrings, "is none of"}}
	catOps  = map[string]opSpec{"exists": {kindCategory, "has"}, "not_exists": {kindCategory, "has not"}}
)

// registry is the single source of truth for validation, SQL and the prompt.
var registry = map[string]fieldSpec{
	"city":                   {desc: "customer city (values from the provided city list)", ops: listOps, column: "city"},
	"language":               {desc: "customer language code (values from the provided language list)", ops: listOps, column: "language"},
	"email_consent":          {desc: "customer opted in to email", ops: boolOps, column: "email_consent"},
	"sms_consent":            {desc: "customer opted in to SMS", ops: boolOps, column: "sms_consent"},
	"cart_after_last_order":  {desc: "customer added something to cart after their latest order (or has never ordered)", ops: boolOps},
	"order_count":            {desc: "lifetime number of orders", ops: numOps, column: "order_count", min: 0, max: 100000, integer: true},
	"total_spent":            {desc: "lifetime spend in TRY", ops: numOps, column: "total_spent", min: 0, max: 100000000},
	"days_since_last_order":  {desc: "days since latest order", ops: dayOps, column: "last_order_at", min: 0, max: 3650, integer: true},
	"days_since_last_cart":   {desc: "days since latest add-to-cart", ops: dayOps, column: "last_cart_at", min: 0, max: 3650, integer: true},
	"days_since_created":     {desc: "days since the customer signed up", ops: dayOps, column: "created_at", min: 0, max: 3650, integer: true},
	"days_since_last_active": {desc: "days since the customer was last active", ops: dayOps, column: "last_active_at", min: 0, max: 3650, integer: true},
	"bought_category":        {desc: "has an order in a product category (set category, within_days)", ops: catOps, table: "orders"},
	"carted_category":        {desc: "added a product category to cart (set category, within_days)", ops: catOps, table: "cart_additions"},
}

const (
	maxConditions = 10
	maxListItems  = 20
	maxWithinDays = 3650
)
