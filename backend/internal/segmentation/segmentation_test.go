package segmentation

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var testCat = Catalog{Cities: []string{"Istanbul", "Ankara"}, Languages: []string{"tr", "en"}, Categories: []string{"fashion", "electronics"}}

func alt(match string, cs ...Condition) Alternative {
	return Alternative{Name: "Test audience", Reasoning: "A reason long enough.", Match: match, Conditions: cs}
}

func n(v ...float64) []float64 { return v }

func TestValidateStructure(t *testing.T) {
	ok := alt("all", Condition{Field: "city", Op: "in", Strings: []string{"Istanbul"}})
	if err := Validate(ok, testCat); err != nil {
		t.Fatalf("valid alternative rejected: %v", err)
	}
	bad := map[string]Alternative{
		"unknown field":       alt("all", Condition{Field: "country", Op: "in", Strings: []string{"US"}}),
		"unknown op":          alt("all", Condition{Field: "city", Op: "like", Strings: []string{"Istanbul"}}),
		"sql in value":        alt("all", Condition{Field: "city", Op: "in", Strings: []string{"Istanbul'; DROP TABLE customers;--"}}),
		"missing number":      alt("all", Condition{Field: "total_spent", Op: "gte"}),
		"fractional count":    alt("all", Condition{Field: "order_count", Op: "gte", Numbers: n(2.5)}),
		"fractional days":     alt("all", Condition{Field: "days_since_last_cart", Op: "lte", Numbers: n(7.5)}),
		"stray strings":       alt("all", Condition{Field: "order_count", Op: "gte", Numbers: n(2), Strings: []string{"x"}}),
		"stray category":      alt("all", Condition{Field: "sms_consent", Op: "is_true", Category: "fashion"}),
		"stray numbers":       alt("all", Condition{Field: "bought_category", Op: "exists", Category: "fashion", Numbers: n(1)}),
		"range inverted":      alt("all", Condition{Field: "total_spent", Op: "between", Numbers: n(10, 1)}),
		"days out of range":   alt("all", Condition{Field: "days_since_last_cart", Op: "lte", Numbers: n(99999)}),
		"unknown category":    alt("all", Condition{Field: "bought_category", Op: "exists", Category: "luxury"}),
		"no conditions":       alt("all"),
		"bad match":           alt("xor", Condition{Field: "sms_consent", Op: "is_true"}),
		"short name":          {Name: "A", Reasoning: "A reason long enough.", Match: "all", Conditions: ok.Conditions},
		"missing explanation": {Name: "Fine name", Reasoning: "", Match: "all", Conditions: ok.Conditions},
	}
	for name, a := range bad {
		err := Validate(a, testCat)
		var p *Problem
		if !errors.As(err, &p) {
			t.Errorf("%s: expected a Problem, got %v", name, err)
			continue
		}
		// marketer-facing text must not leak internal names
		for _, jargon := range []string{"needs 1", "unsupported field", "_", "number(s)"} {
			if strings.Contains(p.User, jargon) {
				t.Errorf("%s: user message leaks %q: %s", name, jargon, p.User)
			}
		}
	}
}

func TestValidateLogic(t *testing.T) {
	contradictions := map[string]Alternative{
		"order count 0 and >=5":          alt("all", Condition{Field: "order_count", Op: "eq", Numbers: n(0)}, Condition{Field: "order_count", Op: "gte", Numbers: n(5)}),
		"spend >=10000 and <=1000":       alt("all", Condition{Field: "total_spent", Op: "gte", Numbers: n(10000)}, Condition{Field: "total_spent", Op: "lte", Numbers: n(1000)}),
		"city in and not in":             alt("all", Condition{Field: "city", Op: "in", Strings: []string{"Istanbul"}}, Condition{Field: "city", Op: "not_in", Strings: []string{"Istanbul"}}),
		"disjoint cities":                alt("all", Condition{Field: "city", Op: "in", Strings: []string{"Istanbul"}}, Condition{Field: "city", Op: "in", Strings: []string{"Ankara"}}),
		"never ordered but recent order": alt("all", Condition{Field: "order_count", Op: "eq", Numbers: n(0)}, Condition{Field: "days_since_last_order", Op: "lte", Numbers: n(7)}),
		"never ordered but bought":       alt("all", Condition{Field: "days_since_last_order", Op: "is_null"}, Condition{Field: "bought_category", Op: "exists", Category: "fashion"}),
		"consent true and false":         alt("all", Condition{Field: "sms_consent", Op: "is_true"}, Condition{Field: "sms_consent", Op: "is_false"}),
		"bought and never bought":        alt("all", Condition{Field: "bought_category", Op: "exists", Category: "fashion", WithinDays: 30}, Condition{Field: "bought_category", Op: "not_exists", Category: "fashion"}),
		"never carted but open cart":     alt("all", Condition{Field: "days_since_last_cart", Op: "is_null"}, Condition{Field: "cart_after_last_order", Op: "is_true"}),
	}
	for name, a := range contradictions {
		if err := Validate(a, testCat); err == nil || !strings.Contains(err.Error(), "contradiction") {
			t.Errorf("%s: expected contradiction, got %v", name, err)
		}
	}
	fine := map[string]Alternative{
		"any can mix":                  alt("any", Condition{Field: "order_count", Op: "eq", Numbers: n(0)}, Condition{Field: "order_count", Op: "gte", Numbers: n(5)}),
		"bought long ago, not lately":  alt("all", Condition{Field: "bought_category", Op: "exists", Category: "fashion"}, Condition{Field: "bought_category", Op: "not_exists", Category: "fashion", WithinDays: 90}),
		"impossible-looking but valid": alt("all", Condition{Field: "city", Op: "in", Strings: []string{"Ankara"}}, Condition{Field: "order_count", Op: "gte", Numbers: n(500)}),
	}
	for name, a := range fine {
		if err := Validate(a, testCat); err != nil {
			t.Errorf("%s: unexpected rejection: %v", name, err)
		}
	}
}

func TestNormalizeDropsRedundant(t *testing.T) {
	c := Condition{Field: "city", Op: "in", Strings: []string{"Istanbul"}}
	a, notes := Normalize(alt("all", c, c, Condition{Field: "total_spent", Op: "gte", Numbers: n(0)}))
	if len(a.Conditions) != 1 || len(notes) != 2 {
		t.Fatalf("got %d conditions, notes %v", len(a.Conditions), notes)
	}
}

func TestCompileUsesBindsOnly(t *testing.T) {
	a := alt("all",
		Condition{Field: "city", Op: "in", Strings: []string{"Istanbul"}},
		Condition{Field: "cart_after_last_order", Op: "is_true"},
		Condition{Field: "days_since_last_cart", Op: "lte", Numbers: n(30)},
		Condition{Field: "bought_category", Op: "not_exists", Category: "fashion", WithinDays: 60},
	)
	where, args := Compile(a, time.Now())
	if strings.Contains(where, "Istanbul") || strings.Contains(where, "fashion") {
		t.Fatalf("value leaked into SQL: %s", where)
	}
	if len(args) != 4 || !strings.Contains(where, "$4") {
		t.Fatalf("unexpected binds: %d args, sql %s", len(args), where)
	}
}

// Case 15: malformed model output never reaches validation or SQL.
func TestParseReply(t *testing.T) {
	cond := `{"field":"city","op":"in","strings":["Istanbul"],"numbers":[],"category":"","within_days":0}`
	altJSON := `{"name":"Core","reasoning":"r","match":"all","conditions":[` + cond + `]}`
	wrap := func(outcome, alts, extra string) string {
		return `{"outcome":"` + outcome + `","message":"m","questions":[],"unsupported_requirements":[],"suggestions":[],"alternatives":[` + alts + `]` + extra + `}`
	}
	if _, alts, err := parseReply(wrap("supported", altJSON, "")); err != nil || len(alts) != 1 {
		t.Fatalf("valid reply rejected: %v", err)
	}
	if as, _, err := parseReply(wrap("unsupported", "", "")); err != nil || as.Outcome != OutcomeUnsupported {
		t.Fatalf("unsupported reply rejected: %v", err)
	}
	bad := map[string]string{
		"unknown top-level property": wrap("supported", altJSON, `,"sql":"SELECT 1"`),
		"unknown condition property": wrap("supported", strings.Replace(altJSON, `"within_days":0`, `"within_days":0,"raw":"x"`, 1), ""),
		"unknown outcome":            wrap("maybe", altJSON, ""),
		"supported without options":  wrap("supported", "", ""),
		"not json":                   "sure, here you go",
		"clarification with nothing": `{"outcome":"needs_clarification","message":"","questions":[],"unsupported_requirements":[],"suggestions":[],"alternatives":[]}`,
	}
	for name, raw := range bad {
		if _, _, err := parseReply(raw); !errors.Is(err, ErrBadReply) {
			t.Errorf("%s: expected ErrBadReply, got %v", name, err)
		}
	}
}
