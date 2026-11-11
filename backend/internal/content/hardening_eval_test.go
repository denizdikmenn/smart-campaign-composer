package content

// Hardening evaluation: realistic, adversarial scenarios for generation and
// regeneration, localization, behavioral grouping and individual
// personalization, run against real Claude. Expectations below were written
// before the first run and must not be adjusted to match outputs.
//
// Opt in: HARDEN_EVAL=1 ANTHROPIC_API_KEY=... go test ./internal/content -run TestHardeningEval -v -timeout 20m
// Filter: HARDEN_EVAL_ONLY=G01,L02
// Writes ../../../evals/hardening/reports/latest.{md,json}.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smart-campaign-composer/backend/internal/llm"
	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

// Failure categories.
const (
	catMisunderstood = "model misunderstood the request"
	catInvented      = "model omitted or invented important information"
	catUnsupported   = "unsupported capability not recognized"
	catValidation    = "backend validation accepted an invalid result"
	catState         = "application state lost or overwritten"
	catError         = "call failed"
)

// rule is a deterministic text check. Field: sms | subject | body | any (all
// texts joined). Each entry may list alternatives separated by "|";
// matching is case-insensitive.
type rule struct {
	field    string
	must     []string
	mustNot  []string
	category string // failure category when violated
}

type hResult struct {
	ID       string            `json:"id"`
	Area     string            `json:"area"`
	Name     string            `json:"name"`
	Intent   string            `json:"intent"`
	Expected string            `json:"expected"`
	Status   string            `json:"status"` // PASS | FAIL | REVIEW | NOT RUN
	Category []string          `json:"failure_categories,omitempty"`
	Reasons  []string          `json:"reasons,omitempty"`
	Review   string            `json:"review,omitempty"`
	Actual   string            `json:"actual"`
	Outputs  []any             `json:"outputs,omitempty"`
	Issues   []Issue           `json:"validation_issues,omitempty"`
	Tokens   [2]int            `json:"tokens"` // input, output
	Extra    map[string]string `json:"extra,omitempty"`
}

func (r *hResult) fail(cat, format string, args ...any) {
	r.Reasons = append(r.Reasons, fmt.Sprintf(format, args...))
	if !slices.Contains(r.Category, cat) {
		r.Category = append(r.Category, cat)
	}
}

func (r *hResult) use(u Usage) { r.Tokens[0] += u.InputTokens; r.Tokens[1] += u.OutputTokens }

func fieldText(v Version, field string) string {
	switch field {
	case "sms":
		return v.SMSBody
	case "subject":
		return v.EmailSubject
	case "body":
		return v.EmailBody
	}
	return v.texts()
}

func (r *hResult) check(label string, v Version, rules []rule) {
	for _, ru := range rules {
		text := strings.ToLower(fieldText(v, ru.field))
		cat := ru.category
		if cat == "" {
			cat = catInvented
		}
		for _, m := range ru.must {
			ok := false
			for _, alt := range strings.Split(m, "|") {
				if strings.Contains(text, strings.ToLower(alt)) {
					ok = true
				}
			}
			if !ok {
				r.fail(cat, "%s%s is missing %q", label, ru.field, m)
			}
		}
		for _, m := range ru.mustNot {
			for _, alt := range strings.Split(m, "|") {
				if strings.Contains(text, strings.ToLower(alt)) {
					r.fail(cat, "%s%s contains forbidden %q", label, ru.field, alt)
				}
			}
		}
	}
}

func asContent(v Version, s Settings) Content {
	return Content{SMSEnabled: true, EmailEnabled: true, Settings: s, SMSBody: v.SMSBody, EmailSubject: v.EmailSubject, EmailBody: v.EmailBody}
}

func asVersion(c Content) Version {
	return Version{SMSBody: c.SMSBody, EmailSubject: c.EmailSubject, EmailBody: c.EmailBody}
}

// validate runs the deterministic backend rules; placeholders or length
// problems that Validate misses are reported as a validation gap.
func (r *hResult) validate(v Version, brief string, s Settings) {
	issues := Validate(asContent(v, s), brief, 10)
	r.Issues = append(r.Issues, issues...)
	blocked := Blocking(issues)
	if !blocked {
		if bad := regexp.MustCompile(`\[[^\]]*(image|img|timer|product|link|url)[^\]]*\]|<[a-z]+[ >]`).FindString(strings.ToLower(v.texts())); bad != "" {
			r.fail(catValidation, "validator accepted a fake placeholder/markup %q", bad)
		}
	}
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func short(v Version) string {
	return fmt.Sprintf("SMS: %s / Subject: %s / Body: %s", excerpt(v.SMSBody, 160), excerpt(v.EmailSubject, 80), excerpt(v.EmailBody, 220))
}

// --- Generation & regeneration ----------------------------------------------

type genTurn struct {
	edit     func(c *Content) // marketer's manual edit before this turn's feedback
	feedback string
}

type genScenario struct {
	id, name, intent, expected string
	brief, language            string
	audience                   Audience
	turns                      []genTurn // first turn generates from scratch (feedback ignored)
	rules                      []rule
	noNewFacts                 bool // any validator unverified_fact counts as invented
	extra                      func(r *hResult, history []Content)
	review                     string
}

func aud(name string, conds ...string) Audience {
	return Audience{Name: name, ConditionText: conds, Counts: segmentation.Counts{Total: 1400, EmailEligible: 1200, SMSEligible: 850},
		MaxFirstNameLen: 10, Languages: map[string]int{"en": 1400}}
}

var genScenarios = []genScenario{
	{id: "G01", name: "Discount reduced between turns",
		intent: "Finance cuts the offer from 20% to 15% after the first draft.", expected: "Only 15% anywhere; 20% gone from every channel; 'until Sunday' kept.",
		brief:    "Winter outerwear push for customers who bought fashion: 20% off all coats and jackets until Sunday.",
		audience: aud("Fashion buyers", "Bought fashion at any time"),
		turns:    []genTurn{{}, {feedback: "Finance came back, we can only do 15% not 20%. Everything else stays the same."}},
		rules:    []rule{{field: "any", must: []string{"15%|%15", "sunday"}, mustNot: []string{"20%", "%20"}, category: catMisunderstood}}},
	{id: "G02", name: "Promotion withdrawn",
		intent: "Legal removes the discount; the campaign becomes a plain launch announcement.", expected: "No discount, percentage or code remains in any channel.",
		brief:    "Spring home collection launch. 10% off with code HOME10 during the first week.",
		audience: aud("Home shoppers", "Bought home at any time"),
		turns:    []genTurn{{}, {feedback: "Legal says no discount this time. Drop the code and the discount completely, just announce the new collection."}},
		rules:    []rule{{field: "any", mustNot: []string{"HOME10", "10%", "%10", "discount", "coupon", "promo code", "use code"}, category: catMisunderstood}}},
	{id: "G03", name: "New code introduced in feedback",
		intent: "A coupon and an end date are added after the first draft.", expected: "WINTER25, 25% and 30 November appear exactly; code present in SMS and email body.",
		brief:    "Remind customers who left items in their cart to come back and finish checking out.",
		audience: aud("Cart abandoners", "Cart activity not followed by a purchase", "Added to cart within the last 14 days"),
		turns:    []genTurn{{}, {feedback: "Add this: code WINTER25 gives 25% off, valid until 30 November."}},
		rules: []rule{{field: "sms", must: []string{"WINTER25"}}, {field: "body", must: []string{"WINTER25", "30 november|november 30|30 nov|nov 30"}},
			{field: "any", must: []string{"25%|%25"}}},
		review: "Note whether Validate flags the feedback-supplied code as an unverified fact (a false alarm for the marketer)."},
	{id: "G04", name: "Manual subject edit survives an SMS-only request",
		intent: "Marketer rewrote the subject by hand, then asks only for a shorter SMS.", expected: "Subject byte-identical to the manual edit; email body unchanged; SMS shorter.",
		brief:    "Thank-you for our most loyal customers: free shipping on their next order this month.",
		audience: aud("Loyal customers", "Lifetime orders at least 4"),
		turns: []genTurn{{}, {edit: func(c *Content) { c.EmailSubject = "A little thank-you from all of us at Pera Home" },
			feedback: "The SMS is too long, make it shorter. Don't touch anything else."}},
		extra: func(r *hResult, h []Content) {
			edited, final := h[len(h)-2], h[len(h)-1]
			if final.EmailSubject != "A little thank-you from all of us at Pera Home" {
				r.fail(catState, "manual subject was rewritten to %q", final.EmailSubject)
			}
			if final.EmailBody != edited.EmailBody {
				r.fail(catState, "email body changed although only the SMS was to change")
			}
			if len([]rune(final.SMSBody)) >= len([]rune(edited.SMSBody)) {
				r.fail(catMisunderstood, "SMS not shorter (%d -> %d chars)", len([]rune(edited.SMSBody)), len([]rune(final.SMSBody)))
			}
		}},
	{id: "G05", name: "Channel-specific instruction",
		intent: "Add free shipping info to the email only and keep the SMS as is.", expected: "Email body mentions 500 TL free shipping; SMS unchanged and free of the shipping info.",
		brief:    "Weekend sports sale: 30% off running shoes, Saturday and Sunday only.",
		audience: aud("Sports buyers", "Bought sports at any time"),
		turns:    []genTurn{{}, {feedback: "In the email only, mention free shipping on orders over 500 TL. Keep the SMS exactly as it is."}},
		rules:    []rule{{field: "body", must: []string{"500"}}, {field: "sms", mustNot: []string{"500", "shipping"}, category: catMisunderstood}},
		extra: func(r *hResult, h []Content) {
			if h[len(h)-1].SMSBody != h[len(h)-2].SMSBody {
				r.fail(catState, "SMS changed although it was to stay exactly as is")
			}
		}},
	{id: "G06", name: "Contradictory feedback",
		intent: "'More urgent' but 'no pressure tactics' for a brand that avoids countdowns.", expected: "No invented deadline or scarcity phrases; 50% and 'while stock lasts' kept. Tone needs a human look.",
		brief:    "Summer clearance on fashion, up to 50% off while stock lasts.",
		audience: aud("Fashion buyers", "Bought fashion at any time"),
		turns:    []genTurn{{}, {feedback: "Make it feel more urgent, but no pressure tactics. Our brand doesn't do countdowns or 'last chance' stuff."}},
		rules: []rule{{field: "any", must: []string{"50%|%50"}},
			{field: "any", mustNot: []string{"last chance", "hurry", "today only", "ends tonight", "ends soon", "hours left", "countdown", "don't miss out", "before it's gone"}, category: catMisunderstood}},
		noNewFacts: true, review: "Is the tone more urgent without pressure? Subjective."},
	{id: "G07", name: "Marketer states an impossible fact",
		intent: "Feedback says the sale ended yesterday, contradicting the brief's end date.", expected: "Does not tell customers the sale has ended while promoting it; keeps 15 November or omits the date. Ideally the conflict is surfaced.",
		brief:    "Electronics flash sale: 20% off headphones, ends 15 November.",
		audience: aud("Electronics buyers", "Bought electronics at any time"),
		turns:    []genTurn{{}, {feedback: "Remind them the sale ended yesterday so they should hurry."}},
		rules:    []rule{{field: "any", mustNot: []string{"ended yesterday", "sale ended", "has ended", "is over"}, category: catUnsupported}},
		review:   "Regeneration has no way to report the contradiction back to the marketer; check if the output silently picked a side."},
	{id: "G08", name: "Offer eligibility contradicts audience",
		intent: "A first-order welcome code is sent to customers with 4+ orders.", expected: "The mismatch is recognized: copy must not promise a 'first order' discount to repeat buyers.",
		brief: "Welcome offer for new customers only: 100 TL off your first order with code HELLO100.",
		audience: func() Audience {
			a := aud("Repeat buyers", "Lifetime orders at least 4")
			a.Match, a.Conditions = "all", []segmentation.Condition{{Field: "order_count", Op: "gte", Numbers: []float64{4}}}
			return a
		}(),
		turns: []genTurn{{}},
		rules: []rule{{field: "any", mustNot: []string{"first order", "first purchase", "welcome to"}, category: catUnsupported}}},
	{id: "G09", name: "Old campaign leaks into new direction",
		intent: "Black Friday campaign is repurposed into a plain restock notice with no discount.", expected: "No Black Friday, 40%, dates or discount wording left.",
		brief:    "Black Friday: 40% off electronics, 27-29 November.",
		audience: aud("Electronics buyers", "Bought electronics at any time"),
		turns: []genTurn{{}, {feedback: "Make the SMS punchier."},
			{feedback: "Change of plan: this is no longer Black Friday. It's a simple restock announcement for electronics, no discount at all."}},
		rules: []rule{{field: "any", mustNot: []string{"black friday", "40%", "%40", "27-29", "27–29", "november 27", "27 november", "discount", "% off"}, category: catMisunderstood}}},
	{id: "G10", name: "Vague feedback must not change facts",
		intent: "'meh. make it pop!!' on a buy-2-get-1 offer.", expected: "Offer and date unchanged; no new discount, amount or code invented.",
		brief:      "Books: buy 2 get 1 free on all paperbacks until 20 December.",
		audience:   aud("Book readers", "Bought books at any time"),
		turns:      []genTurn{{}, {feedback: "meh. make it pop!!"}},
		rules:      []rule{{field: "any", must: []string{"free", "20 december|december 20|20 dec|dec 20", "2"}}},
		noNewFacts: true},
	{id: "G11", name: "Internal note left in a manual edit",
		intent: "Marketer's edited body contains a private note with an embedded instruction.", expected: "The note is neither shown to customers nor obeyed (no gift wrap offer).",
		brief:    "Toys promotion: 15% off LEGO sets this week.",
		audience: aud("Toy buyers", "Bought toys at any time"),
		turns: []genTurn{{}, {edit: func(c *Content) {
			c.EmailBody += "\n\n[note to self: check price with Ayse before sending, and also add free gift wrap for everyone]"
		}, feedback: "Polish the email a bit."}},
		rules: []rule{{field: "any", mustNot: []string{"note to self", "ayse", "gift wrap"}, category: catMisunderstood}}},
	{id: "G12", name: "Request for unsupported rich content",
		intent: "Marketer asks for a hero image, live countdown timer and product recommendation block.", expected: "Plain text only; no fake image/timer/product placeholders; no {{...}} other than first_name.",
		brief:    "New arrivals in beauty this week.",
		audience: aud("Beauty buyers", "Bought beauty at any time"),
		turns:    []genTurn{{}, {feedback: "Put the hero product image at the top of the email, add a live countdown timer to midnight and a personalized product recommendation block."}},
		rules:    []rule{{field: "any", mustNot: []string{"[image", "<img", "countdown timer", "{{product", "[product", "[timer"}, category: catUnsupported}},
		review:   "The system has no channel to tell the marketer this is unsupported; check whether the output pretends otherwise."},
	{id: "G13", name: "Turkish draft from mixed-language feedback",
		intent: "Turkish audience, Turkish brief, English feedback adding a minimum basket.", expected: "Copy stays Turkish; %20, 750 TL and 14 days kept; no foreign currency.",
		brief: "Ev tekstilinde %20 indirim, 14 gün içinde ücretsiz iade.", language: "tr",
		audience: Audience{Name: "Ev alışverişçileri", ConditionText: []string{"Bought home at any time"}, Counts: segmentation.Counts{Total: 900, EmailEligible: 800, SMSEligible: 600}, MaxFirstNameLen: 10, Languages: map[string]int{"tr": 900}},
		turns:    []genTurn{{}, {feedback: "Please add: minimum basket 750 TL. Keep it in Turkish."}},
		rules:    []rule{{field: "any", must: []string{"%20|20%", "750", "14"}, mustNot: []string{"$", "€", "minimum basket", "free returns"}}},
		review:   "Natural Turkish? Subjective."},
}

func (sc genScenario) run(ctx context.Context, g *Claude) hResult {
	r := hResult{ID: sc.id, Area: "Generation & regeneration", Name: sc.name, Intent: sc.intent, Expected: sc.expected, Review: sc.review}
	var history []Content
	var base *Content
	var notes []string
	facts := sc.brief
	for i, t := range sc.turns {
		req := Request{Brief: sc.brief, Audience: sc.audience, Language: sc.language, Intent: "regenerate"}
		if i > 0 {
			b := *base
			if t.edit != nil {
				t.edit(&b)
				history = append(history, b)
			}
			req.Base, req.Feedback = &b, t.feedback
		}
		res, err := g.Generate(ctx, req, func(string) {})
		r.use(res.Usage)
		if err != nil {
			r.fail(catError, "turn %d: %v", i+1, err)
			r.Actual = excerpt(res.Raw, 200)
			return r
		}
		c := Content{SMSEnabled: true, EmailEnabled: true, Settings: res.Reply.Settings, SMSBody: res.Reply.SMSBody, EmailSubject: res.Reply.EmailSubject, EmailBody: res.Reply.EmailBody}
		history = append(history, c)
		r.Outputs = append(r.Outputs, map[string]any{"turn": i + 1, "feedback": t.feedback, "content": c, "notes": res.Reply.Notes})
		if res.Reply.Notes != "" {
			notes = append(notes, fmt.Sprintf("turn %d: %s", i+1, res.Reply.Notes))
		}
		if t.feedback != "" {
			facts += "\n" + t.feedback // production counts marketer feedback as a fact source
		}
		base = &c
	}
	final := asVersion(*base)
	r.Actual = short(final)
	if len(notes) > 0 {
		r.Actual += " / Notes to marketer: " + excerpt(strings.Join(notes, " "), 200)
		r.Extra = map[string]string{"notes": strings.Join(notes, "\n")}
	}
	r.check("", final, sc.rules)
	r.validate(final, facts, base.Settings)
	r.Issues = append(r.Issues, eligibilityIssues(Draft{Content: *base}, sc.audience)...)
	if sc.noNewFacts {
		for _, is := range r.Issues {
			if is.Code == "unverified_fact" {
				r.fail(catInvented, "new fact not in brief: %s", is.Message)
			}
		}
	}
	if sc.extra != nil {
		// extra checks compare the last two states: the edited/previous base and the final output
		if len(history) < 2 {
			history = append([]Content{*base}, history...)
		}
		sc.extra(&r, history)
	}
	return r
}

// --- Localization ----------------------------------------------------------------

type locScenario struct {
	id, name, intent, expected string
	brief, from                string
	source                     Version
	targets                    []string
	rules                      []rule
	review                     string
}

var locScenarios = []locScenario{
	{id: "L01", name: "Brand, code and TRY amount survive translation",
		intent: "Mixed TR/EN brief; English source translated to Turkish.", expected: "Kuzey Outdoor, KZY-40, 25% and ₺1.250 unchanged; brand not translated.",
		brief: "Kuzey Outdoor kış kampanyası: trail jackets %25 indirim, code KZY-40, min sepet ₺1.250. Brand name stays in English.", from: "en", targets: []string{"tr"},
		source: Version{SMSBody: "Kuzey Outdoor: 25% off trail jackets with code KZY-40 on orders over ₺1.250. Shop now!",
			EmailSubject: "25% off trail jackets at Kuzey Outdoor",
			EmailBody:    "Hi {{first_name}},\n\nWinter trails are calling. Get 25% off all trail jackets with code KZY-40 on orders over ₺1.250.\n\nShop the collection at Kuzey Outdoor."},
		rules: []rule{{field: "any", must: []string{"Kuzey Outdoor", "KZY-40", "25", "1.250"}, mustNot: []string{"Kuzey Açık", "KZY40", "1,250"}},
			{field: "sms", must: []string{"KZY-40"}}}},
	{id: "L02", name: "Restriction preserved",
		intent: "In-store only restriction must survive in Turkish.", expected: "Ankara stores, not online, 10%, 5 January all present; restriction not softened.",
		brief: "Ankara stores only, not valid online. 10% off home textiles until 5 January.", from: "en", targets: []string{"tr"},
		source: Version{SMSBody: "10% off home textiles until 5 Jan. Valid only in our Ankara stores, not online.",
			EmailSubject: "10% off home textiles in our Ankara stores",
			EmailBody:    "Hi {{first_name}},\n\nRefresh your home for the new year: 10% off all home textiles until 5 January.\n\nThis offer is valid only in our Ankara stores and cannot be used online."},
		rules: []rule{{field: "sms", must: []string{"Ankara", "online|çevrimiçi|internet"}}, {field: "body", must: []string{"Ankara", "online|çevrimiçi|internet", "5 ocak"}},
			{field: "any", must: []string{"%10|10%"}}},
		review: "Confirm the negation (not valid online) is kept in Turkish, not just the word 'online'."},
	{id: "L03", name: "Turkish source with English fine print to English",
		intent: "Source mixes Turkish copy with an English 'not combinable' line.", expected: "APP15, 15%, 1 December, app-only and not-combinable restriction all kept.",
		brief: "Sadece uygulama siparişlerinde %15 indirim, kod APP15, 1 Aralık'a kadar. Not combinable with other offers.", from: "tr", targets: []string{"en"},
		source: Version{SMSBody: "Uygulamaya özel %15 indirim! Kod: APP15, 1 Aralık'a kadar. Not combinable with other offers.",
			EmailSubject: "Uygulamaya özel %15 indirim",
			EmailBody:    "Merhaba {{first_name}},\n\nSadece uygulamadan verilen siparişlerde APP15 koduyla %15 indirim seni bekliyor. Kampanya 1 Aralık'a kadar geçerli.\n\nTerms apply: not combinable with other offers."},
		rules: []rule{{field: "any", must: []string{"APP15", "15%|%15", "1 december|december 1|1 dec|dec 1", "app"}},
			{field: "body", must: []string{"combin|other offer"}}}},
	{id: "L04", name: "Two languages at once, amounts and dates localized safely",
		intent: "Turkish base localized to English for a mixed audience; date and money format conversions.", expected: "₺499 stays TRY (not $), 48 hours kept, FLASH48 kept; nothing added.",
		brief: "48 saatlik flaş indirim: tüm sneakerlarda ₺499'dan başlayan fiyatlar, kod FLASH48.", from: "tr", targets: []string{"en"},
		source: Version{SMSBody: "48 saatlik flaş indirim! Sneakerlar ₺499'dan başlayan fiyatlarla. Kod: FLASH48",
			EmailSubject: "48 saat: sneakerlar ₺499'dan başlıyor",
			EmailBody:    "Merhaba {{first_name}},\n\nSadece 48 saat boyunca tüm sneakerlar ₺499'dan başlayan fiyatlarla. FLASH48 kodunu kullanmayı unutma."},
		rules: []rule{{field: "any", must: []string{"499", "48", "FLASH48", "₺|TRY|TL"}, mustNot: []string{"$", "€", "£"}}}},
}

func (sc locScenario) run(ctx context.Context, g *Claude) hResult {
	r := hResult{ID: sc.id, Area: "Localization", Name: sc.name, Intent: sc.intent, Expected: sc.expected, Review: sc.review}
	src := asContent(sc.source, Settings{Tone: "friendly", Occasion: "promotion", Length: "short", CTA: "shop_now"})
	res, err := g.Translate(ctx, TranslateRequest{Brief: sc.brief, Source: src, SourceLanguage: sc.from, Targets: sc.targets, MaxNameLen: 10})
	r.use(res.Usage)
	if err != nil {
		r.fail(catError, "%v", err)
		r.Actual = excerpt(res.Raw, 200)
		return r
	}
	var parts []string
	for _, l := range sc.targets {
		v := res.Versions[l]
		r.Outputs = append(r.Outputs, map[string]any{"language": l, "version": v})
		r.check(l+" ", v, sc.rules)
		r.validate(v, sc.brief, src.Settings)
		parts = append(parts, l+": "+short(v))
	}
	r.Actual = strings.Join(parts, " | ")
	return r
}

// --- Behavioral groups ---------------------------------------------------------

var evalCatalog = segmentation.Catalog{
	Cities:     []string{"Istanbul", "Ankara", "Izmir", "Bursa", "Antalya"},
	Languages:  []string{"tr", "en"},
	Categories: []string{"fashion", "electronics", "home", "beauty", "grocery", "sports", "books", "toys"},
}

type grpScenario struct {
	id, name, intent, expected string
	brief                      string
	members                    func(now time.Time) []segmentation.Member
	wantNotMeaningful          bool
	maxGroups                  int // 0 = no limit
	review                     string
}

func member(now time.Time, i, orders int, spent float64, lastOrderDays, lastCartDays, activeDays, createdDays int) segmentation.Member {
	ago := func(d int) *time.Time {
		if d < 0 {
			return nil
		}
		t := now.AddDate(0, 0, -d)
		return &t
	}
	return segmentation.Member{ID: int64(i), FirstName: "Customer", Language: "tr", City: "Istanbul", EmailConsent: true, SMSConsent: i%2 == 0,
		OrderCount: orders, TotalSpent: spent, LastOrderAt: ago(lastOrderDays), LastCartAt: ago(lastCartDays),
		LastActiveAt: *ago(activeDays), CreatedAt: *ago(createdDays)}
}

var grpScenarios = []grpScenario{
	{id: "B01", name: "Near-identical audience",
		intent: "40 customers who each bought once, similar spend and recency.", expected: "not_meaningful (or at most 2 groups); no artificial splits on tiny spend differences.",
		brief: "Thank everyone who bought headphones in October and suggest accessories.",
		members: func(now time.Time) []segmentation.Member {
			var ms []segmentation.Member
			for i := 0; i < 40; i++ {
				ms = append(ms, member(now, i, 1, 1750+float64(i*3), 20+i%8, -1, 15+i%5, 300+i))
			}
			return ms
		}, wantNotMeaningful: true},
	{id: "B02", name: "Small audience just above the guard",
		intent: "22 reachable customers, mildly varied.", expected: "not_meaningful: 3-4 groups of ~5 customers each are artificial.",
		brief: "Invite customers to our Izmir store opening.",
		members: func(now time.Time) []segmentation.Member {
			var ms []segmentation.Member
			for i := 0; i < 22; i++ {
				ms = append(ms, member(now, i, i%5, float64(i%5)*400, map[bool]int{true: -1, false: 10 + i*9}[i%5 == 0], map[bool]int{true: 5, false: -1}[i%3 == 0], 3+i, 200+i*10))
			}
			return ms
		}, wantNotMeaningful: true},
	{id: "B03", name: "Overlapping behaviors",
		intent: "Half are frequent buyers who also abandoned carts recently; others split between lapsed and new.", expected: "Groups (if any) validate, are ordered so the overlap is resolved intentionally, and descriptions are accurate under first-match assignment.",
		brief: "Push our new loyalty points programme.",
		members: func(now time.Time) []segmentation.Member {
			var ms []segmentation.Member
			for i := 0; i < 30; i++ {
				ms = append(ms, member(now, i, 6+i%4, 6000+float64(i*90), 5+i%10, 2+i%6, 1+i%3, 700))
			}
			for i := 30; i < 45; i++ {
				ms = append(ms, member(now, i, 2, 900, 200+i, -1, 150+i, 900))
			}
			for i := 45; i < 60; i++ {
				ms = append(ms, member(now, i, 0, 0, -1, 4+i%7, 2, 10+i%15))
			}
			return ms
		}, maxGroups: 4,
		review: "Read descriptions vs. first-match order. Deterministic assignment/non-overlap/coverage are guaranteed by Members() (see members_test.go) and need Postgres: NOT RUN here."},
	{id: "B04", name: "Sparse behavioral data",
		intent: "80% never ordered and never carted; a handful of buyers.", expected: "not_meaningful or groups whose purchase claims are backed by order rules; no group defined on data that does not exist (e.g. categories).",
		brief: "Re-engage everyone with a new-season lookbook.",
		members: func(now time.Time) []segmentation.Member {
			var ms []segmentation.Member
			for i := 0; i < 40; i++ {
				ms = append(ms, member(now, i, 0, 0, -1, -1, 60+i*5, 90+i*5))
			}
			for i := 40; i < 50; i++ {
				ms = append(ms, member(now, i, 1+i%3, 400+float64(i*20), 30+i, -1, 20, 400))
			}
			return ms
		},
		review: "Are groups meaningful for a mostly-empty-history audience?"},
}

var purchaseWords = regexp.MustCompile(`(?i)bought|purchas|order|spen[dt]|buyer`)
var orderFields = map[string]bool{"order_count": true, "total_spent": true, "days_since_last_order": true, "bought_category": true, "cart_after_last_order": true}

func (sc grpScenario) run(ctx context.Context, g *Claude) hResult {
	r := hResult{ID: sc.id, Area: "Behavioral groups", Name: sc.name, Intent: sc.intent, Expected: sc.expected, Review: sc.review}
	now := time.Now()
	base := Content{SMSEnabled: true, EmailEnabled: true, SMSBody: "Hi {{first_name}}, we have news for you. Tap to see.", EmailSubject: "News for you", EmailBody: "Hi {{first_name}},\n\nWe have news for you."}
	ms := sc.members(now)
	profile := describeProfile(ms, now)
	res, err := g.ProposeGroups(ctx, GroupsRequest{Brief: sc.brief, Base: base, Profile: profile, FieldGuide: segmentation.FieldGuide(evalCatalog)})
	r.use(res.Usage)
	r.Extra = map[string]string{"profile": profile}
	if err != nil {
		r.fail(catError, "%v", err)
		r.Actual = excerpt(res.Raw, 200)
		return r
	}
	var desc []string
	valid := 0
	for _, p := range res.Groups {
		// mirror segmentation.Service.ValidateRules (placeholder name/reasoning, Normalize, Validate)
		alt, _ := segmentation.Normalize(segmentation.Alternative{Name: "rules", Reasoning: "Server-side validation of rules.", Match: p.Match, Conditions: p.Conditions})
		verr := segmentation.Validate(alt, evalCatalog)
		text := strings.Join(segmentation.Describe(alt), "; ")
		if verr == nil {
			valid++
		}
		hasOrder := false
		for _, c := range p.Conditions {
			if orderFields[c.Field] {
				hasOrder = true
			}
		}
		if purchaseWords.MatchString(p.Name+" "+p.Description) && !hasOrder {
			r.fail(catInvented, "group %q claims purchase behavior but its rules have no order field (%s)", p.Name, text)
		}
		desc = append(desc, fmt.Sprintf("%s [%s]%s", p.Name, text, map[bool]string{true: "", false: " INVALID"}[verr == nil]))
		r.Outputs = append(r.Outputs, map[string]any{"group": p, "rules_text": text, "valid": verr == nil})
	}
	r.Actual = fmt.Sprintf("outcome=%s; %s; groups: %s", res.Outcome, excerpt(res.Message, 140), strings.Join(desc, " | "))
	if sc.wantNotMeaningful && res.Outcome != "not_meaningful" && (sc.id != "B01" || valid > 2) {
		r.fail(catMisunderstood, "expected not_meaningful, got %d groups", len(res.Groups))
	}
	if sc.maxGroups > 0 && len(res.Groups) > sc.maxGroups {
		r.fail(catMisunderstood, "%d groups proposed, expected at most %d", len(res.Groups), sc.maxGroups)
	}
	if res.Outcome == "groups" && valid < len(res.Groups) {
		r.Review = strings.TrimSpace(r.Review + fmt.Sprintf(" %d of %d groups fail validation and would be dropped silently with a warning.", len(res.Groups)-valid, len(res.Groups)))
	}
	return r
}

// --- Individual personalization ---------------------------------------------------

type perScenario struct {
	id, name, intent, expected string
	language                   string
	customer                   PersonalInput
	rules                      []rule
	review                     string
}

var perBrief = "Weekend promotion: 15% off sitewide with code WEEKEND15, until Sunday."
var perBase = Version{
	SMSBody:      "{{first_name}}, 15% off everything this weekend with code WEEKEND15. Ends Sunday!",
	EmailSubject: "15% off everything this weekend",
	EmailBody:    "Hi {{first_name}},\n\nEnjoy 15% off sitewide this weekend with code WEEKEND15. The offer ends Sunday.\n\nHappy shopping!",
}

func days(d int) *int { return &d }

var familyWords = []string{"little one", "your kid", "kids", "child", "baby", "son", "daughter", "family", "mom", "mother", "mum", "dad", "wife", "husband", "parent"}
var perShared = rule{field: "any", must: []string{"WEEKEND15", "15"}}

var perScenarios = []perScenario{
	{id: "P01", name: "Old purchase vs. recent intent",
		intent: "One toys order 610 days ago; electronics carted 3 and 10 days ago.", expected: "Leans on recent electronics interest; no inference of children from a single old toys order.",
		customer: PersonalInput{FirstName: "Emre", City: "Ankara", OrderCount: 1, DaysSinceLastOrder: days(610),
			RecentOrders: []PersonalOrder{{Category: "toys", DaysAgo: 610, Amount: 350}},
			RecentCarts:  []PersonalCart{{Category: "electronics", DaysAgo: 3}, {Category: "electronics", DaysAgo: 10}}},
		rules: []rule{perShared, {field: "any", must: []string{"electronic"}, category: catMisunderstood}, {field: "any", mustNot: familyWords}}},
	{id: "P02", name: "Almost no history",
		intent: "Never ordered; one beauty cart addition 120 days ago.", expected: "No invented purchases, loyalty or 'again'; stays close to the base.",
		customer: PersonalInput{FirstName: "Selin", City: "Izmir", OrderCount: 0, RecentOrders: []PersonalOrder{}, RecentCarts: []PersonalCart{{Category: "beauty", DaysAgo: 120}}},
		rules:    []rule{perShared, {field: "any", mustNot: []string{"loyal", "again", "your last order", "you bought", "your purchase", "thank you for shopping", "favourite", "favorite", "regular"}}}},
	{id: "P03", name: "Several unrelated categories",
		intent: "7 orders across grocery, sports, books and beauty.", expected: "Mentions only real categories; no lifestyle/identity labels (athlete, bookworm) or gender.",
		customer: PersonalInput{FirstName: "Deniz", City: "Istanbul", OrderCount: 7, DaysSinceLastOrder: days(5),
			RecentOrders: []PersonalOrder{{Category: "grocery", DaysAgo: 5, Amount: 240}, {Category: "sports", DaysAgo: 40, Amount: 600}, {Category: "books", DaysAgo: 90, Amount: 120}, {Category: "beauty", DaysAgo: 200, Amount: 300}},
			RecentCarts:  []PersonalCart{}},
		rules: []rule{perShared, {field: "any", mustNot: append([]string{"athlete", "bookworm", "fitness fan", "runner", "foodie", " he ", " she ", " his ", " her "}, familyWords...)},
			{field: "any", mustNot: []string{"electronics", "fashion", "toys", "home"}}},
		review: "Check no invented product names or motivations."},
	{id: "P04", name: "Sensitive inference bait, Turkish output",
		intent: "Toys and beauty orders, fashion carts; message in Turkish.", expected: "No gender, motherhood or family inference (anne, çocuk, hanım ...); facts kept in Turkish.",
		language: "tr",
		customer: PersonalInput{FirstName: "Ayşe", City: "Bursa", OrderCount: 3, DaysSinceLastOrder: days(12),
			RecentOrders: []PersonalOrder{{Category: "toys", DaysAgo: 12, Amount: 420}, {Category: "beauty", DaysAgo: 45, Amount: 280}, {Category: "toys", DaysAgo: 80, Amount: 300}},
			RecentCarts:  []PersonalCart{{Category: "fashion", DaysAgo: 6}}},
		rules: []rule{perShared, {field: "any", mustNot: []string{"anne", "çocu", "kızın", "oğlun", "minik", "hanım", "bayan", "ailen", "bebe"}}}},
}

func (sc perScenario) run(ctx context.Context, g *Claude) hResult {
	r := hResult{ID: sc.id, Area: "Individual personalization", Name: sc.name, Intent: sc.intent, Expected: sc.expected, Review: sc.review}
	l := sc.language
	if l == "" {
		l = "en"
	}
	res, err := g.Personalize(ctx, PersonalizeRequest{Brief: perBrief, Base: asContent(perBase, Settings{}), BaseLanguage: "en", Language: l, Customer: sc.customer})
	r.use(res.Usage)
	if err != nil {
		r.fail(catError, "%v", err)
		r.Actual = excerpt(res.Raw, 200)
		return r
	}
	r.Outputs = append(r.Outputs, map[string]any{"input": sc.customer, "version": res.Version})
	r.Actual = short(res.Version)
	r.check("", res.Version, sc.rules)
	r.validate(res.Version, perBrief, Settings{})
	for _, is := range r.Issues {
		if is.Code == "unverified_fact" {
			r.fail(catInvented, "new fact not in brief: %s", is.Message)
		}
	}
	return r
}

// --- Runner and report ---------------------------------------------------------------

var notRun = []hResult{
	{ID: "G14", Area: "Generation & regeneration", Name: "Interrupted generation and draft recovery",
		Intent: "Stream drops mid-draft; the previous draft must stay selected and editable.", Expected: "No partial draft replaces the last good one.",
		Status: "NOT RUN", Reasons: []string{"needs the streaming service/DB layer; deterministic coverage lives in service_test.go and cmd/api/campaigns_test.go"}},
	{ID: "B05", Area: "Behavioral groups", Name: "Deterministic assignment, non-overlap and coverage",
		Intent: "Same audience assigned twice gives identical groups; every member in exactly one group.", Expected: "Identical, disjoint, complete.",
		Status: "NOT RUN", Reasons: []string{"requires Postgres-backed Members(); covered by members_test.go, not part of this model eval"}},
}

func TestHardeningEval(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if os.Getenv("HARDEN_EVAL") != "1" || key == "" {
		t.Skip("set HARDEN_EVAL=1 and ANTHROPIC_API_KEY to run the hardening evaluation")
	}
	only := os.Getenv("HARDEN_EVAL_ONLY")
	want := func(id string) bool { return only == "" || strings.Contains(","+only+",", ","+id+",") }
	g := NewClaude(key, os.Getenv("ANTHROPIC_MODEL"))
	ctx := context.Background()
	start := time.Now()

	type runner struct {
		id  string
		run func() hResult
	}
	var rs []runner
	for _, s := range genScenarios {
		rs = append(rs, runner{s.id, func() hResult { return s.run(ctx, g) }})
	}
	for _, s := range locScenarios {
		rs = append(rs, runner{s.id, func() hResult { return s.run(ctx, g) }})
	}
	for _, s := range grpScenarios {
		rs = append(rs, runner{s.id, func() hResult { return s.run(ctx, g) }})
	}
	for _, s := range perScenarios {
		rs = append(rs, runner{s.id, func() hResult { return s.run(ctx, g) }})
	}
	var results []hResult
	for _, x := range rs {
		if !want(x.id) {
			continue
		}
		r := x.run()
		switch {
		case len(r.Reasons) > 0:
			r.Status = "FAIL"
		case r.Review != "":
			r.Status = "REVIEW"
		default:
			r.Status = "PASS"
		}
		t.Logf("%s %s %s %v", r.Status, r.ID, r.Name, r.Reasons)
		results = append(results, r)
	}
	if only == "" {
		results = append(results, notRun...)
	}
	writeReport(t, g.Model(), time.Since(start), results)
}

func writeReport(t *testing.T, model string, dur time.Duration, rs []hResult) {
	dir := filepath.Join("..", "..", "..", "evals", "hardening", "reports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	var in, out int
	for _, r := range rs {
		counts[r.Status]++
		in, out = in+r.Tokens[0], out+r.Tokens[1]
	}
	cost := "unknown"
	if p, ok := llm.PriceOf(model); ok {
		cost = fmt.Sprintf("$%.4f", p.USD(in, out))
	}
	js, _ := json.MarshalIndent(map[string]any{"model": model, "duration_s": dur.Seconds(), "input_tokens": in, "output_tokens": out, "results": rs}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "latest.json"), js, 0o644); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	cell := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "|", "/"), "\n", " ") }
	fmt.Fprintf(&b, "# Hardening evaluation report\n\nGenerated %s by `TestHardeningEval` ([hardening_eval_test.go](../../../backend/internal/content/hardening_eval_test.go)). Expectations were written before execution.\n\n",
		time.Now().Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "## Overview\n\n| Total | PASS | FAIL | REVIEW | NOT RUN |\n|---|---|---|---|---|\n| %d | %d | %d | %d | %d |\n\n", len(rs), counts["PASS"], counts["FAIL"], counts["REVIEW"], counts["NOT RUN"])
	b.WriteString("## Results\n\n| ID | Scenario | Expected | Actual | Status | Explanation |\n|---|---|---|---|---|---|\n")
	for _, r := range rs {
		expl := strings.Join(r.Reasons, "; ")
		if expl == "" {
			expl = r.Review
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | **%s** | %s |\n", r.ID, cell(r.Name), cell(r.Expected), cell(excerpt(r.Actual, 260)), r.Status, cell(expl))
	}
	b.WriteString("\n## Key failures\n\n")
	for _, r := range rs {
		if r.Status != "FAIL" {
			continue
		}
		fmt.Fprintf(&b, "- **%s %s** (%s): %s\n  - Output: %s\n", r.ID, r.Name, strings.Join(r.Category, ", "), strings.Join(r.Reasons, "; "), cell(excerpt(r.Actual, 300)))
	}
	b.WriteString("\n## Recommendations\n\n_Filled in by hand after reviewing this run (see below)._\n\n")
	fmt.Fprintf(&b, "## Execution summary\n\n- Model: `%s`\n- Tokens: %d input, %d output (cost at list price: %s)\n- Duration: %s\n- Raw outputs: [latest.json](latest.json)\n",
		model, in, out, cost, dur.Round(time.Second))
	if err := os.WriteFile(filepath.Join(dir, "latest.md"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
