package segmentation

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Catalog holds the values that exist in the database; rules may only
// reference these.
type Catalog struct {
	Cities     []string
	Languages  []string
	Categories []string
}

// Problem is a validation failure with a marketer-facing message and a
// technical detail that only goes to traces and logs.
type Problem struct {
	User string // safe to show: plain language, no field/operator jargon
	Tech string
}

func (p *Problem) Error() string { return p.Tech }

func problem(user, tech string, args ...any) *Problem {
	return &Problem{User: user, Tech: fmt.Sprintf(tech, args...)}
}

// Validate checks an alternative's structure against the registry and
// catalog, then its logic (contradictions). Call Normalize first to drop
// redundant conditions.
func Validate(a Alternative, cat Catalog) error {
	if n := len(strings.TrimSpace(a.Name)); n < 3 || n > 80 {
		return problem("it has no usable name", "name must be 3-80 characters")
	}
	if n := len(strings.TrimSpace(a.Reasoning)); n < 10 || n > 600 {
		return problem("it has no usable explanation", "reasoning must be 10-600 characters")
	}
	if a.Match != "all" && a.Match != "any" {
		return problem("it combines its conditions in an unsupported way", "match must be \"all\" or \"any\", got %q", a.Match)
	}
	if len(a.Conditions) == 0 {
		return problem("it has no targeting conditions", "needs at least 1 condition")
	}
	if len(a.Conditions) > maxConditions {
		return problem("it has too many conditions to be reliable", "needs at most %d conditions", maxConditions)
	}
	for i, c := range a.Conditions {
		if p := validateCondition(c, cat); p != nil {
			p.Tech = fmt.Sprintf("condition %d (%s %s): %s", i+1, c.Field, c.Op, p.Tech)
			return p
		}
	}
	if p := checkLogic(a); p != nil {
		return p
	}
	return nil
}

func isInt(v float64) bool { return v == math.Trunc(v) }

func validateCondition(c Condition, cat Catalog) *Problem {
	spec, ok := registry[c.Field]
	if !ok {
		return problem("it relies on customer data we don't have", "unsupported field")
	}
	op, ok := spec.ops[c.Op]
	if !ok {
		return problem("it uses a comparison we can't apply to "+fieldLabel(c.Field), "unsupported operator")
	}
	// Value fields that the operator does not use must be empty, so nothing
	// the model wrote is silently ignored.
	if op.kind != kindStrings && len(c.Strings) > 0 {
		return problem("it contains values that don't fit "+fieldLabel(c.Field), "unexpected strings")
	}
	if op.kind != kindNumber && op.kind != kindBetween && len(c.Numbers) > 0 {
		return problem("it contains numbers that don't fit "+fieldLabel(c.Field), "unexpected numbers")
	}
	if op.kind != kindCategory && (c.Category != "" || c.WithinDays != 0) {
		return problem("it contains a product category where none applies", "unexpected category/within_days")
	}
	switch op.kind {
	case kindStrings:
		if len(c.Strings) == 0 || len(c.Strings) > maxListItems {
			return problem("its "+fieldLabel(c.Field)+" list is empty or too long", "needs 1-%d values", maxListItems)
		}
		allowed := cat.Cities
		if c.Field == "language" {
			allowed = cat.Languages
		}
		for _, v := range c.Strings {
			if !slices.Contains(allowed, v) {
				return problem(fmt.Sprintf("it refers to %s %q, which doesn't exist in our customer data", fieldLabel(c.Field), v), "unknown value %q", v)
			}
		}
	case kindNumber, kindBetween:
		n := 1
		if op.kind == kindBetween {
			n = 2
		}
		if len(c.Numbers) != n {
			return problem("a condition on "+fieldLabel(c.Field)+" is missing its number", "needs %d number(s), got %d", n, len(c.Numbers))
		}
		for _, v := range c.Numbers {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < spec.min || v > spec.max {
				return problem("a value for "+fieldLabel(c.Field)+" is out of range", "value %v out of range", v)
			}
			if spec.integer && !isInt(v) {
				return problem(fieldLabel(c.Field)+" must be a whole number", "value %v is not an integer", v)
			}
		}
		if n == 2 && c.Numbers[0] > c.Numbers[1] {
			return problem("a range for "+fieldLabel(c.Field)+" starts after it ends", "range start is after its end")
		}
	case kindCategory:
		if !slices.Contains(cat.Categories, c.Category) {
			return problem(fmt.Sprintf("it refers to the product category %q, which we don't sell", c.Category), "unknown category %q", c.Category)
		}
		if c.WithinDays < 0 || c.WithinDays > maxWithinDays {
			return problem("its time window is out of range", "within_days out of range")
		}
	}
	return nil
}

// fieldLabel is a plain-language name for a field.
func fieldLabel(field string) string {
	switch field {
	case "total_spent":
		return "lifetime spend"
	case "order_count":
		return "number of orders"
	case "cart_after_last_order":
		return "open cart activity"
	}
	return strings.ReplaceAll(strings.TrimPrefix(field, "days_since_"), "_", " ")
}

// Describe renders conditions as short human-readable phrases.
func Describe(a Alternative) []string {
	out := make([]string, 0, len(a.Conditions))
	for _, c := range a.Conditions {
		out = append(out, describe(c))
	}
	return out
}

func describe(c Condition) string {
	label := strings.ReplaceAll(c.Field, "_", " ")
	num := func(v float64) string { return fmt.Sprintf("%g", v) }
	switch c.Field {
	case "city", "language":
		verb := "is"
		if c.Op == "not_in" {
			verb = "is not"
		}
		return fmt.Sprintf("%s %s %s", label, verb, strings.Join(c.Strings, " / "))
	case "email_consent", "sms_consent":
		if c.Op == "is_true" {
			return strings.ReplaceAll(label, "consent", "opted in")
		}
		return strings.ReplaceAll(label, "consent", "not opted in")
	case "cart_after_last_order":
		if c.Op == "is_true" {
			return "cart activity not followed by a purchase"
		}
		return "no pending cart activity"
	case "bought_category", "carted_category":
		verb := map[string]string{"bought_category": "bought", "carted_category": "added to cart"}[c.Field]
		neg := ""
		if c.Op == "not_exists" {
			neg = "did not buy/add "
			verb = ""
		}
		w := ""
		if c.WithinDays > 0 {
			w = fmt.Sprintf(" in the last %d days", c.WithinDays)
		}
		return strings.TrimSpace(fmt.Sprintf("%s%s %s%s", neg, verb, c.Category, w))
	case "order_count", "total_spent":
		unit := ""
		if c.Field == "total_spent" {
			unit = " TRY"
		}
		switch c.Op {
		case "eq":
			return fmt.Sprintf("%s = %s%s", label, num(c.Numbers[0]), unit)
		case "gte":
			return fmt.Sprintf("%s ≥ %s%s", label, num(c.Numbers[0]), unit)
		case "lte":
			return fmt.Sprintf("%s ≤ %s%s", label, num(c.Numbers[0]), unit)
		default:
			return fmt.Sprintf("%s %s–%s%s", label, num(c.Numbers[0]), num(c.Numbers[1]), unit)
		}
	default: // days_since_*
		what := strings.TrimPrefix(label, "days since ")
		switch c.Op {
		case "lte":
			return fmt.Sprintf("%s within %s days", what, num(c.Numbers[0]))
		case "gte":
			return fmt.Sprintf("%s more than %s days ago", what, num(c.Numbers[0]))
		case "between":
			return fmt.Sprintf("%s %s–%s days ago", what, num(c.Numbers[0]), num(c.Numbers[1]))
		case "is_null":
			return "never " + what
		default:
			return "has " + what
		}
	}
}
