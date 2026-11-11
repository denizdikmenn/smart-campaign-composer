package content

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Channel limits and personalization.
const (
	MaxSMS       = 160
	MaxSubject   = 80
	FirstNameVar = "{{first_name}}"
	// nameFallback bounds personalized length when the audience's longest
	// first name is unknown (e.g. an empty audience).
	nameFallback = 15
	maxFieldLen  = 10000 // bytes; abuse guard for edited content
)

// Issue is one validation finding. Blocking issues prevent approval;
// warnings are shown but do not block.
type Issue struct {
	Field    string `json:"field"` // channels | sms_body | email_subject | email_body
	Code     string `json:"code"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
	// Language of the content version the issue is about ("" = not language specific).
	Language string `json:"language,omitempty"`
	// Scope names the content unit at review time, e.g. "Variant B" or a group name.
	Scope string `json:"scope,omitempty"`
}

// Blocking reports whether any issue blocks approval.
func Blocking(issues []Issue) bool {
	for _, i := range issues {
		if i.Blocking {
			return true
		}
	}
	return false
}

var placeholderRe = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// Render substitutes the supported personalization variables.
func Render(template, firstName string) string {
	return strings.ReplaceAll(template, FirstNameVar, firstName)
}

// PersonalizedLen is the length in characters of template after substitution
// with a first name of nameLen characters.
func PersonalizedLen(template string, nameLen int) int {
	n := strings.Count(template, FirstNameVar)
	return utf8.RuneCountInString(template) + n*(nameLen-utf8.RuneCountInString(FirstNameVar))
}

// Validate checks a draft against the channel rules. maxNameLen is the longest
// first name among reachable audience members (0 = unknown). Enabled channels
// only; facts are checked against the brief and only produce warnings.
func Validate(c Content, brief string, maxNameLen int) []Issue {
	if maxNameLen <= 0 {
		maxNameLen = nameFallback
	}
	issues := []Issue{}
	add := func(field, code string, blocking bool, msg string, args ...any) {
		issues = append(issues, Issue{Field: field, Code: code, Message: fmt.Sprintf(msg, args...), Blocking: blocking})
	}
	if !c.SMSEnabled && !c.EmailEnabled {
		add("channels", "no_channel", true, "Enable at least one channel.")
	}
	type field struct {
		name, label, text string
		limit             int // personalized character limit; 0 = none
	}
	var fields []field
	if c.SMSEnabled {
		fields = append(fields, field{"sms_body", "SMS", c.SMSBody, MaxSMS})
	}
	if c.EmailEnabled {
		fields = append(fields, field{"email_subject", "Email subject", c.EmailSubject, MaxSubject}, field{"email_body", "Email body", c.EmailBody, 0})
	}
	for _, f := range fields {
		if strings.TrimSpace(f.text) == "" {
			add(f.name, "required", true, "%s is empty.", f.label)
			continue
		}
		if len(f.text) > maxFieldLen {
			add(f.name, "too_long", true, "%s is too long.", f.label)
			continue
		}
		bad := false
		for _, p := range placeholderRe.FindAllString(f.text, -1) {
			if p != FirstNameVar {
				add(f.name, "unsupported_variable", true, "%s uses %s. Only %s is supported.", f.label, p, FirstNameVar)
				bad = true
			}
		}
		if rest := placeholderRe.ReplaceAllString(f.text, ""); strings.Contains(rest, "{{") || strings.Contains(rest, "}}") {
			add(f.name, "malformed_variable", true, "%s has an unclosed variable. Write it as %s.", f.label, FirstNameVar)
			bad = true
		}
		if f.limit == 0 || bad {
			continue
		}
		raw := utf8.RuneCountInString(f.text)
		personal := PersonalizedLen(f.text, maxNameLen)
		switch {
		case raw > f.limit && !strings.Contains(f.text, FirstNameVar):
			add(f.name, "too_long", true, "%s is %d characters; the limit is %d.", f.label, raw, f.limit)
		case personal > f.limit:
			add(f.name, "too_long_personalized", true,
				"%s is %d characters for the longest first name in this audience (%d letters); the limit is %d.", f.label, personal, maxNameLen, f.limit)
		}
	}
	for _, f := range fields {
		for _, fact := range unsupportedFacts(f.text, brief) {
			add(f.name, "unverified_fact", false, "%s mentions “%s”, which is not in the brief. Make sure it is a real offer.", f.label, fact)
		}
	}
	return issues
}

var (
	percentRe = regexp.MustCompile(`%\s?(\d+(?:[.,]\d+)?)|(\d+(?:[.,]\d+)?)\s?%`)
	moneyRe   = regexp.MustCompile(`(?i)[₺$€£]\s?(\d+(?:[.,]\d+)?)|(\d+(?:[.,]\d+)?)\s?(?:₺|\$|€|£|TL\b|TRY\b)`)
	codeRe    = regexp.MustCompile(`\b[A-Z0-9]{4,}\b`)
	digitRe   = regexp.MustCompile(`\d`)
	letterRe  = regexp.MustCompile(`[A-Z]`)
)

// unsupportedFacts returns discounts, amounts and coupon-like codes that
// appear in text but not in the brief. Heuristic: a warning, not a guarantee.
func unsupportedFacts(text, brief string) []string {
	var out []string
	seen := map[string]bool{}
	note := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	numbers := func(re *regexp.Regexp, s string) map[string]bool {
		set := map[string]bool{}
		for _, m := range re.FindAllStringSubmatch(s, -1) {
			set[m[1]+m[2]] = true
		}
		return set
	}
	for _, re := range []*regexp.Regexp{percentRe, moneyRe} {
		inBrief := numbers(re, brief)
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			if !inBrief[m[1]+m[2]] {
				note(strings.TrimSpace(m[0]))
			}
		}
	}
	upperBrief := strings.ToUpper(brief)
	for _, code := range codeRe.FindAllString(text, -1) {
		if digitRe.MatchString(code) && letterRe.MatchString(code) && !strings.Contains(upperBrief, code) {
			note(code)
		}
	}
	return out
}

// firstOrderRe matches copy that addresses people who have never ordered.
var firstOrderRe = regexp.MustCompile(`(?i)first[- ](order|purchase|buy|time)|new customers?\b|[iİ]lk (sipariş|alışveriş)|yeni müşteri`)

// hasOrdered reports whether every audience member must have ordered before.
func hasOrdered(a Audience) bool {
	for _, c := range a.Conditions {
		if a.Match == "any" && len(a.Conditions) > 1 {
			return false // alternatives: some members may not have ordered
		}
		low := 0.0
		if len(c.Numbers) > 0 {
			low = c.Numbers[0]
		}
		switch {
		case c.Field == "order_count" && (c.Op == "gte" || c.Op == "eq" || c.Op == "between") && low >= 1,
			c.Field == "total_spent" && (c.Op == "gte" || c.Op == "eq" || c.Op == "between") && low > 0,
			c.Field == "days_since_last_order" && c.Op != "is_null",
			c.Field == "bought_category" && c.Op == "exists":
			return true
		}
	}
	return false
}

// eligibilityIssues flags first-order / new-customer wording sent to an
// audience in which everyone has already ordered: a claim most recipients
// cannot use.
func eligibilityIssues(d Draft, a Audience) []Issue {
	if !hasOrdered(a) {
		return nil
	}
	texts := []string{d.SMSBody, d.EmailSubject, d.EmailBody}
	for _, t := range d.Translations {
		texts = append(texts, t.texts())
	}
	if m := firstOrderRe.FindString(strings.Join(texts, "\n")); m != "" {
		return []Issue{{Field: "audience", Code: "eligibility_mismatch", Blocking: true,
			Message: fmt.Sprintf("The message mentions “%s”, but everyone in this audience has already ordered. Change the offer or the audience.", m)}}
	}
	return nil
}
