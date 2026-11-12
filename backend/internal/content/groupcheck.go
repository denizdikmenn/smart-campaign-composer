package content

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

// Deterministic checks of behavioral group messages (warnings only): groups
// planned with the same angle, groups whose messages are nearly the same, a
// group that just repeats the base message, cart claims a group's rules don't
// guarantee, and groups the content call itself found not worth a different
// message.

const (
	// Word-set overlap between two groups' messages. Calibrated on real
	// output: generic win-back copy for different groups scored 0.6-1.0,
	// genuinely different angles 0.44-0.51.
	similarGroups = 0.55
	similarBase   = 0.8 // overlap with the base message
)

// cartRe matches cart wording in the supported languages.
var cartRe = regexp.MustCompile(`(?i)\b(cart|basket|warenkorb)\b|sepet`)

// words is the set of words (3+ letters) in a message, without the placeholder.
func words(v Version) map[string]bool {
	text := strings.ToLower(strings.ReplaceAll(v.texts(), "{{first_name}}", " "))
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= 3 {
			out[w] = true
		}
	}
	return out
}

// overlap is the Jaccard similarity of two word sets.
func overlap(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	shared := 0
	for w := range a {
		if b[w] {
			shared++
		}
	}
	return float64(shared) / float64(len(a)+len(b)-shared)
}

// guaranteesCart reports whether the rules ensure every member added to cart.
func guaranteesCart(match string, conds []segmentation.Condition) bool {
	for _, c := range conds {
		positive := c.Field == "cart_after_last_order" && c.Op == "is_true" ||
			c.Field == "days_since_last_cart" && (c.Op == "lte" || c.Op == "between" || c.Op == "not_null") ||
			c.Field == "carted_category" && c.Op == "exists"
		if positive && (match != "any" || len(conds) == 1) {
			return true
		}
	}
	return false
}

// checkGroups appends warnings to the generated groups' issues.
func checkGroups(c Campaign, base Draft, hasBase bool, groups []Group) {
	audienceCart := guaranteesCart(c.Audience.Match, c.Audience.Conditions)
	warn := func(g *Group, code, lang, msg string) {
		g.Issues = append(g.Issues, Issue{Field: "group", Code: code, Language: lang, Message: msg})
	}
	for i := range groups {
		g := &groups[i]
		// Two groups planned with the same angle will get the same message,
		// however differently it is worded: flag it before and after generation.
		for _, o := range groups[:i] {
			if !g.remainder && !o.remainder && g.Angle != "" && o.Angle != "" &&
				overlap(words(Version{SMSBody: g.Angle}), words(Version{SMSBody: o.Angle})) >= similarGroups {
				warn(g, "group_same_angle", "", fmt.Sprintf("Planned with the same message angle as “%s”: these groups don't call for different messages. Consider proposing groups again.", o.Name))
			}
		}
		if g.Status != "generated" {
			continue
		}
		if g.Differentiated != nil && !*g.Differentiated && !g.remainder {
			warn(g, "group_not_distinct", "", fmt.Sprintf("Claude found nothing in “%s” that justifies a different message, so it is close to the base message. Consider fewer groups.", g.Name))
		}
		cartOK := audienceCart || !g.remainder && guaranteesCart(g.Match, g.Conditions)
		for _, l := range g.languageList() {
			v, ok := g.Content[l]
			if !ok || v.empty() {
				continue
			}
			if !cartOK && cartRe.MatchString(v.texts()) {
				warn(g, "group_claim_cart", l, "Mentions a cart, but not everyone in this group has added something to their cart.")
			}
			if hasBase && !g.remainder {
				if b, ok := base.TextIn(l); ok && overlap(words(v), words(b)) >= similarBase {
					warn(g, "group_like_base", l, "Barely differs from the base message.")
				}
			}
			// Compare with every earlier group; the later one carries the warning.
			for _, o := range groups[:i] {
				ov, ok := o.Content[l]
				if o.Status != "generated" || !ok || ov.empty() {
					continue
				}
				if overlap(words(v), words(ov)) >= similarGroups {
					warn(g, "group_similar", l, fmt.Sprintf("Nearly the same as the message for “%s”, so this split adds little. Rewrite one of them, or propose groups again.", o.Name))
				}
			}
		}
	}
}
