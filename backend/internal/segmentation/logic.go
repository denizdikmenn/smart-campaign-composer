package segmentation

import (
	"fmt"
	"math"
	"reflect"
	"slices"
)

// Normalize drops conditions that cannot change the audience: exact
// duplicates and lower bounds of zero on non-negative numbers. It returns the
// cleaned alternative and a technical note per dropped condition.
func Normalize(a Alternative) (Alternative, []string) {
	var notes []string
	kept := make([]Condition, 0, len(a.Conditions))
	for _, c := range a.Conditions {
		if slices.ContainsFunc(kept, func(k Condition) bool { return reflect.DeepEqual(k, c) }) {
			notes = append(notes, fmt.Sprintf("dropped duplicate condition %s %s", c.Field, c.Op))
			continue
		}
		if (c.Field == "order_count" || c.Field == "total_spent") && c.Op == "gte" && len(c.Numbers) == 1 && c.Numbers[0] == 0 {
			notes = append(notes, fmt.Sprintf("dropped no-op condition %s >= 0", c.Field))
			continue
		}
		kept = append(kept, c)
	}
	a.Conditions = kept
	return a, notes
}

// bounds is a closed numeric interval.
type bounds struct{ lo, hi float64 }

func (b *bounds) apply(c Condition) {
	switch c.Op {
	case "eq":
		b.lo, b.hi = math.Max(b.lo, c.Numbers[0]), math.Min(b.hi, c.Numbers[0])
	case "gte":
		b.lo = math.Max(b.lo, c.Numbers[0])
	case "lte":
		b.hi = math.Min(b.hi, c.Numbers[0])
	case "between":
		b.lo, b.hi = math.Max(b.lo, c.Numbers[0]), math.Min(b.hi, c.Numbers[1])
	}
}

func conflict(a, b Condition) *Problem {
	return problem(fmt.Sprintf("its requirements contradict each other (%q and %q can't both be true)", describe(a), describe(b)),
		"contradiction: %s %s vs %s %s", a.Field, a.Op, b.Field, b.Op)
}

// checkLogic finds obvious contradictions between conditions that must all
// hold (match "all"). It is deliberately a short list of practical checks,
// not a solver. With match "any", a contradiction cannot empty the audience.
func checkLogic(a Alternative) *Problem {
	if a.Match != "all" {
		return nil
	}
	num := map[string]*bounds{}     // numeric fields and day counts
	first := map[string]Condition{} // first condition seen per field, for messages
	var nulls, notNulls = map[string]Condition{}, map[string]Condition{}
	bools := map[string]Condition{}
	inSets := map[string][]string{}
	notIn := map[string][]string{}
	exists := map[string]Condition{} // field|category
	notExists := map[string]Condition{}

	for _, c := range a.Conditions {
		if _, ok := first[c.Field]; !ok {
			first[c.Field] = c
		}
		spec := registry[c.Field]
		op := spec.ops[c.Op]
		switch {
		case op.kind == kindNumber || op.kind == kindBetween:
			b, ok := num[c.Field]
			if !ok {
				b = &bounds{lo: 0, hi: math.Inf(1)}
				num[c.Field] = b
			}
			b.apply(c)
			if b.lo > b.hi {
				return conflict(first[c.Field], c)
			}
		case c.Op == "is_null":
			nulls[c.Field] = c
		case c.Op == "not_null":
			notNulls[c.Field] = c
		case c.Op == "is_true" || c.Op == "is_false":
			if prev, ok := bools[c.Field]; ok && prev.Op != c.Op {
				return conflict(prev, c)
			}
			bools[c.Field] = c
		case c.Op == "in":
			if prev, ok := inSets[c.Field]; ok {
				inSets[c.Field] = intersect(prev, c.Strings)
			} else {
				inSets[c.Field] = c.Strings
			}
			if len(inSets[c.Field]) == 0 {
				return conflict(first[c.Field], c)
			}
		case c.Op == "not_in":
			notIn[c.Field] = append(notIn[c.Field], c.Strings...)
		case c.Op == "exists":
			exists[c.Field+"|"+c.Category] = c
		case c.Op == "not_exists":
			notExists[c.Field+"|"+c.Category] = c
		}
	}

	// A day field can't be "never" and also have a date.
	for f, n := range nulls {
		if _, ok := num[f]; ok {
			return conflict(n, first[f])
		}
		if nn, ok := notNulls[f]; ok {
			return conflict(n, nn)
		}
	}
	// Inclusion fully cancelled by exclusion.
	for f, in := range inSets {
		if rest := slices.DeleteFunc(slices.Clone(in), func(v string) bool { return slices.Contains(notIn[f], v) }); len(rest) == 0 {
			return problem(fmt.Sprintf("it both includes and excludes the same %s", fieldLabel(f)), "contradiction: %s in/not_in cancel out", f)
		}
	}
	// "Has X (in window)" vs "has no X (in a window at least as wide)".
	for k, e := range exists {
		if ne, ok := notExists[k]; ok && (ne.WithinDays == 0 || (e.WithinDays > 0 && ne.WithinDays >= e.WithinDays)) {
			return conflict(e, ne)
		}
	}

	// Cross-field facts: no orders means no order dates, purchases or spend.
	noOrders := num["order_count"] != nil && num["order_count"].hi == 0
	if c, ok := nulls["days_since_last_order"]; ok {
		noOrders = true
		if b := num["order_count"]; b != nil && b.lo >= 1 {
			return conflict(c, first["order_count"])
		}
	}
	if noOrders {
		base := first["order_count"]
		if c, ok := nulls["days_since_last_order"]; ok && base.Field == "" {
			base = c
		}
		if _, ok := num["days_since_last_order"]; ok {
			return conflict(base, first["days_since_last_order"])
		}
		if c, ok := notNulls["days_since_last_order"]; ok {
			return conflict(base, c)
		}
		if b := num["total_spent"]; b != nil && b.lo > 0 {
			return conflict(base, first["total_spent"])
		}
		for _, e := range exists {
			if e.Field == "bought_category" {
				return conflict(base, e)
			}
		}
	}
	// Never added to cart vs cart activity.
	if n, ok := nulls["days_since_last_cart"]; ok {
		if c, ok := bools["cart_after_last_order"]; ok && c.Op == "is_true" {
			return conflict(n, c)
		}
		for _, e := range exists {
			if e.Field == "carted_category" {
				return conflict(n, e)
			}
		}
	}
	return nil
}

func intersect(a, b []string) []string {
	var out []string
	for _, v := range a {
		if slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	return out
}
