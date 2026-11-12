package content

// Behavioral group content scenarios for the hardening evaluation (real
// Claude; see hardening_eval_test.go for how to run). Each scenario fixes the
// groups and their members, writes every group's messages exactly as the
// service does (groupRequests → GroupContent), then applies the same
// deterministic checks the UI shows (checkGroups) plus scenario rules.
// Expectations were written before the first run.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

type gcGroup struct {
	name, description, angle string
	match                    string
	conds                    []segmentation.Condition
	members                  func(now time.Time) []segmentation.Member
	rules                    []rule // checked on every language version
}

type gcScenario struct {
	id, name, intent, expected string
	brief                      string
	base                       Draft
	audience                   Audience
	groups                     []gcGroup // rule groups in order; the remainder is added when it has members
	remainder                  func(now time.Time) []segmentation.Member
	remainderRules             []rule
	allRules                   []rule // every group, every language
	weak                       bool   // the split is weak: success means the system recognizes it
	review                     string
}

func num(field, op string, n ...float64) segmentation.Condition {
	return segmentation.Condition{Field: field, Op: op, Numbers: n, Strings: []string{}}
}
func flag(field, op string) segmentation.Condition {
	return segmentation.Condition{Field: field, Op: op, Numbers: []float64{}, Strings: []string{}}
}

// many builds n members with i-dependent variation.
func many(n, from int, f func(now time.Time, i int) segmentation.Member) func(time.Time) []segmentation.Member {
	return func(now time.Time) []segmentation.Member {
		var out []segmentation.Member
		for i := from; i < from+n; i++ {
			m := f(now, i)
			if i%3 == 0 {
				m.Language = "en"
			}
			out = append(out, m)
		}
		return out
	}
}

// No behavior may be claimed for customers who share none.
// ("loyal customer", not "loyal": a loyalty programme can be the campaign's subject, see C03.)
var noBehaviorClaims = rule{field: "any", mustNot: []string{"cart", "basket", "sepet", "you spent", "most valued", "vip", "loyal customer", "sadık müşteri", "en değerli", "over a year", "bir yılı"}}

var lapsedBaseTR = Draft{Language: "tr", Translations: map[string]Version{"en": {
	SMSBody:      "{{first_name}}, we miss you! Would you like to browse our electronics range again?",
	EmailSubject: "We miss you, welcome back to the world of electronics",
	EmailBody:    "Hello {{first_name}},\n\nWe haven't seen you in a while and we miss you. Thank you for choosing our electronics before.\n\nIf you'd like to browse our electronics range again, we'd be happy to help.\n\nThe team"}},
	Content: Content{SMSEnabled: true, EmailEnabled: true, Settings: Settings{Tone: "friendly", Occasion: "win_back", Length: "balanced", CTA: "come_back"},
		SMSBody: lapsedBase.SMSBody, EmailSubject: lapsedBase.EmailSubject, EmailBody: lapsedBase.EmailBody}}

var gcScenarios = []gcScenario{
	{id: "C01", name: "Lapsed electronics buyers (reported case)",
		intent:   "Win-back with no offer; groups by cart intent, value, dormancy; Turkish and English.",
		expected: "Four clearly different messages: cart reminder only for the cart group, a valued-customer tone for high spenders (no cart), rediscovery of what's new for the long dormant, a plain general message for everyone else. No offers invented.",
		brief:    "Win back customers who bought electronics before but have not ordered in the last 3 months.",
		base:     lapsedBaseTR,
		audience: Audience{Match: "all", ConditionText: []string{"bought electronics", "last order more than 90 days ago"}, MaxFirstNameLen: 10,
			Conditions: []segmentation.Condition{{Field: "bought_category", Op: "exists", Category: "electronics"}, num("days_since_last_order", "gte", 90)}},
		groups: []gcGroup{
			{name: "Recent cart intent", description: "Customers who added items to their cart after their last order.",
				angle: "Remind them of the items they added to their cart and invite them to complete the purchase.",
				match: "all", conds: []segmentation.Condition{flag("cart_after_last_order", "is_true")},
				members: many(85, 0, func(now time.Time, i int) segmentation.Member {
					return member(now, i, 1+i%3, 1500+float64(i*40), 100+i*2, 5+i%20, 3+i%10, 600)
				})},
			{name: "High-value lapsed buyers", description: "High-spending lapsed buyers with no cart activity since their last order.",
				angle: "Acknowledge them as valued past customers with a warmer, more premium tone and invite them to see the current range.",
				match: "all", conds: []segmentation.Condition{num("total_spent", "gte", 6500), flag("cart_after_last_order", "is_false")},
				members: many(84, 100, func(now time.Time, i int) segmentation.Member {
					return member(now, i, 4+i%5, 7000+float64(i*60), 120+i, -1, 60+i%30, 900)
				}),
				rules: []rule{{field: "any", mustNot: []string{"cart", "sepet"}}}},
			{name: "Long-dormant electronics buyers", description: "Customers whose last order was over a year ago.",
				angle: "Invite them to rediscover the brand and see what is new since their last visit.",
				match: "all", conds: []segmentation.Condition{num("days_since_last_order", "gte", 365)},
				members: many(77, 200, func(now time.Time, i int) segmentation.Member {
					return member(now, i, 1+i%2, 800+float64(i*5), 380+i, -1, 300+i, 1200)
				}),
				rules: []rule{{field: "any", mustNot: []string{"cart", "sepet"}}}},
		},
		remainder: many(127, 300, func(now time.Time, i int) segmentation.Member {
			return member(now, i, 1+i%3, 1200+float64(i*4), 100+i%200, -1, 40+i%60, 700)
		}),
		remainderRules: []rule{noBehaviorClaims},
		allRules:       []rule{{field: "any", mustNot: []string{"%", "discount", "indirim", "free shipping", "ücretsiz", "gift", "hediye"}}}},

	{id: "C02", name: "Offer campaign: buyers vs never-ordered carters",
		intent:   "A coded discount for repeat buyers, one-time buyers and people who carted but never ordered.",
		expected: "Offer and code kept exactly in every group; repeat buyers thanked as regulars, one-time buyers given a reason to come back, never-ordered carters invited to make a first order (never welcomed back).",
		brief:    "Spring sale: 30% off everything with code SPRING30 until Sunday.",
		base: Draft{Language: "en", Content: Content{SMSEnabled: true, EmailEnabled: true, Settings: Settings{Tone: "friendly", Occasion: "promotion", Length: "short", CTA: "shop_now"},
			SMSBody: "{{first_name}}, our spring sale is on: 30% off everything with code SPRING30 until Sunday.", EmailSubject: "30% off everything until Sunday",
			EmailBody: "Hi {{first_name}},\n\nOur spring sale is here: 30% off everything with code SPRING30, until Sunday.\n\nHappy shopping!"}},
		audience: Audience{Match: "all", ConditionText: []string{"active in the last 90 days"}, MaxFirstNameLen: 10,
			Conditions: []segmentation.Condition{num("days_since_last_active", "lte", 90)}},
		groups: []gcGroup{
			{name: "Repeat buyers", description: "Customers with four or more orders.", angle: "Thank them as regulars and point them to the sale on what they keep coming back for.",
				match: "all", conds: []segmentation.Condition{num("order_count", "gte", 4)},
				members: many(60, 0, func(now time.Time, i int) segmentation.Member {
					return member(now, i, 4+i%6, 3000+float64(i*50), 10+i, -1, 5, 800)
				})},
			{name: "One-time buyers", description: "Customers with exactly one order.", angle: "Give them a reason to place a second order now.",
				match: "all", conds: []segmentation.Condition{num("order_count", "eq", 1)},
				members: many(70, 100, func(now time.Time, i int) segmentation.Member {
					return member(now, i, 1, 400+float64(i*3), 40+i, -1, 20, 300)
				})},
			{name: "Carted, never ordered", description: "Customers who added to cart recently but never ordered.", angle: "Invite them to make their first order with the items they were considering.",
				match: "all", conds: []segmentation.Condition{num("order_count", "eq", 0), num("days_since_last_cart", "lte", 14)},
				members: many(40, 200, func(now time.Time, i int) segmentation.Member { return member(now, i, 0, 0, -1, 2+i%10, 2, 40+i) }),
				rules:   []rule{{field: "any", mustNot: []string{"welcome back", "again", "your last order", "regular"}, category: catInvented}}},
		},
		allRules: []rule{{field: "sms", must: []string{"SPRING30", "30%"}}, {field: "body", must: []string{"SPRING30", "30%", "sunday"}}}},

	{id: "C03", name: "Overlapping behaviors resolved by order",
		intent:   "High spenders with a recent cart come first; then other recent carters; then high spenders without a cart.",
		expected: "Group 2 must not imply high value (those were taken by group 1); group 3 must not mention a cart; three different angles.",
		brief:    "Remind customers about our new loyalty points programme: every order earns points.",
		base: Draft{Language: "en", Content: Content{SMSEnabled: true, EmailEnabled: true, Settings: Settings{Tone: "friendly", Occasion: "announcement", Length: "short", CTA: "learn_more"},
			SMSBody: "{{first_name}}, every order now earns loyalty points. See how it works.", EmailSubject: "Every order now earns points",
			EmailBody: "Hi {{first_name}},\n\nOur new loyalty programme is live: every order earns points.\n\nSee how it works on our site."}},
		audience: Audience{Match: "all", ConditionText: []string{"at least 1 order"}, MaxFirstNameLen: 10, Conditions: []segmentation.Condition{num("order_count", "gte", 1)}},
		groups: []gcGroup{
			{name: "High spenders with cart", description: "High spenders who added to cart in the last 14 days.", angle: "Show how the items they are considering would already earn them points.",
				match: "all", conds: []segmentation.Condition{num("total_spent", "gte", 5000), num("days_since_last_cart", "lte", 14)},
				members: many(30, 0, func(now time.Time, i int) segmentation.Member {
					return member(now, i, 6, 6000+float64(i*90), 20+i, 2+i%6, 1, 700)
				})},
			{name: "Recent carters", description: "Other customers who added to cart in the last 14 days.", angle: "Point out that completing the order they started earns their first points.",
				match: "all", conds: []segmentation.Condition{num("days_since_last_cart", "lte", 14)},
				members: many(35, 100, func(now time.Time, i int) segmentation.Member {
					return member(now, i, 1+i%2, 600+float64(i*10), 60+i, 3+i%8, 2, 400)
				}),
				rules: []rule{{field: "any", mustNot: []string{"most valued", "top customer", "vip", "premium", "big spender"}, category: catInvented}}},
			{name: "High spenders, no cart", description: "High spenders without recent cart activity.", angle: "Thank them for their past orders and show how much their kind of spending earns.",
				match: "all", conds: []segmentation.Condition{num("total_spent", "gte", 5000)},
				members: many(30, 200, func(now time.Time, i int) segmentation.Member {
					return member(now, i, 5, 5500+float64(i*80), 50+i, -1, 30, 900)
				}),
				rules: []rule{{field: "any", mustNot: []string{"cart", "basket"}}}},
		},
		remainder: many(40, 300, func(now time.Time, i int) segmentation.Member {
			return member(now, i, 1+i%3, 900+float64(i*5), 90+i, -1, 60, 800)
		}),
		remainderRules: []rule{noBehaviorClaims}},

	{id: "C04", name: "Weakly differentiated groups",
		intent:   "Two groups split on a spend threshold with nearly identical behavior (2500-3000 vs 3000+ TRY, same recency).",
		expected: "The system recognizes the weak split: at least one group reported as not differentiated, or flagged as nearly the same as the other. No invented differences.",
		brief:    "Tell customers about our new autumn collection.",
		base: Draft{Language: "en", Content: Content{SMSEnabled: true, EmailEnabled: true, Settings: Settings{Tone: "friendly", Occasion: "announcement", Length: "short", CTA: "explore"},
			SMSBody: "{{first_name}}, our autumn collection is here. Take a look.", EmailSubject: "The autumn collection is here",
			EmailBody: "Hi {{first_name}},\n\nOur new autumn collection has arrived.\n\nTake a look on our site."}},
		audience: Audience{Match: "all", ConditionText: []string{"at least 2 orders"}, MaxFirstNameLen: 10, Conditions: []segmentation.Condition{num("order_count", "gte", 2)}},
		groups: []gcGroup{
			{name: "Spend 3000+", description: "Customers who spent at least 3000 TRY.", angle: "Invite them to explore the new collection.",
				match: "all", conds: []segmentation.Condition{num("total_spent", "gte", 3000)},
				members: many(40, 0, func(now time.Time, i int) segmentation.Member {
					return member(now, i, 3, 3000+float64(i*10), 30+i%10, -1, 10, 500)
				})},
			{name: "Spend 2500-3000", description: "Customers who spent 2500 to 3000 TRY.", angle: "Invite them to explore the new collection.",
				match: "all", conds: []segmentation.Condition{num("total_spent", "between", 2500, 3000)},
				members: many(40, 100, func(now time.Time, i int) segmentation.Member {
					return member(now, i, 3, 2500+float64(i*5), 30+i%10, -1, 10, 500)
				})},
		},
		allRules: []rule{{field: "any", mustNot: []string{"most loyal", "top customer", "vip", "first look", "be the first", "among the first", "early access", "explore first", "see it first"}, category: catInvented}},
		weak:     true},
}

func (sc gcScenario) run(ctx context.Context, g *Claude) hResult {
	r := hResult{ID: sc.id, Area: "Behavioral group content", Name: sc.name, Intent: sc.intent, Expected: sc.expected, Review: sc.review, Extra: map[string]string{}}
	now := time.Now()
	c := Campaign{ID: 1, Brief: sc.brief, Audience: sc.audience, BaseLanguage: sc.base.Language}
	langs := map[string]int{sc.base.Language: 1}
	for l := range sc.base.Translations {
		langs[l] = 1
	}
	var groups []Group
	members := map[int][]segmentation.Member{}
	for i, gg := range sc.groups {
		pos := i + 1
		alt := segmentation.Alternative{Name: gg.name, Match: gg.match, Conditions: gg.conds}
		groups = append(groups, Group{Position: pos, Name: gg.name, Description: gg.description, Angle: gg.angle, Match: gg.match, Conditions: gg.conds,
			ConditionText: segmentation.Describe(alt), Languages: langs, Content: map[string]Version{}, Issues: []Issue{}})
		members[pos] = gg.members(now)
		groups[i].Size = len(members[pos])
	}
	if sc.remainder != nil {
		members[0] = sc.remainder(now)
		groups = append(groups, Group{Position: 0, Name: "Everyone else", Description: "Customers who don't match any group above.", Match: "all",
			ConditionText: []string{"Everyone in the audience not matched by an earlier group"}, Languages: langs, Size: len(members[0]),
			Content: map[string]Version{}, Issues: []Issue{}, remainder: true})
	}

	reqs := groupRequests(c, baseText(c, sc.base), groups, members, now)
	notDistinct := 0
	for i := range groups {
		gr := &groups[i]
		res, err := g.GroupContent(ctx, reqs[gr.Position])
		r.use(res.Usage)
		if err != nil {
			r.fail(catError, "%s: %v", gr.Name, err)
			continue
		}
		gr.Content, gr.Rationale, gr.Status = res.Versions, res.Rationale, "generated"
		d := res.Differentiated
		gr.Differentiated = &d
		if !d && !gr.remainder {
			notDistinct++
		}
		if strings.TrimSpace(res.Rationale) == "" {
			r.fail(catMisunderstood, "%s: no rationale", gr.Name)
		}
		rules := append([]rule{}, sc.allRules...)
		if gr.remainder {
			rules = append(rules, sc.remainderRules...)
		} else {
			rules = append(rules, sc.groups[i].rules...)
		}
		for l, v := range res.Versions {
			r.check(fmt.Sprintf("%s (%s) ", gr.Name, l), v, rules)
			r.validate(v, sc.brief, sc.base.Settings)
			r.Outputs = append(r.Outputs, map[string]any{"group": gr.Name, "language": l, "differentiated": d, "rationale": res.Rationale, "message": v})
		}
		r.Extra[gr.Name] = fmt.Sprintf("differentiated=%v; %s", d, res.Rationale)
	}

	checkGroups(c, sc.base, true, groups)
	flagged := 0
	var summary []string
	for _, gr := range groups {
		for _, is := range gr.Issues {
			if is.Blocking {
				continue
			}
			switch is.Code {
			case "group_similar", "group_like_base", "group_not_distinct":
				flagged++
				if !sc.weak && !(gr.remainder && is.Code == "group_like_base") {
					r.fail(catMisunderstood, "%s (%s): %s", gr.Name, is.Language, is.Message)
				}
			case "group_claim_cart":
				r.fail(catInvented, "%s (%s): %s", gr.Name, is.Language, is.Message)
			}
		}
		if v, ok := gr.Content[sc.base.Language]; ok {
			summary = append(summary, fmt.Sprintf("%s: %s", gr.Name, excerpt(v.SMSBody, 110)))
		}
	}
	if sc.weak && notDistinct == 0 && flagged == 0 {
		r.Review = strings.TrimSpace(r.Review + " Neither group was reported as not differentiated nor flagged as similar: read the rationales and decide whether the variation is real or artificial.")
	}
	r.Actual = strings.Join(summary, " | ")
	return r
}
