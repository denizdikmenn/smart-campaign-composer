package content

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

var (
	cartRule      = segmentation.Condition{Field: "cart_after_last_order", Op: "is_true"}
	noCartRule    = segmentation.Condition{Field: "cart_after_last_order", Op: "is_false"}
	spendRule     = segmentation.Condition{Field: "total_spent", Op: "gte", Numbers: []float64{6500}}
	dormantRule   = segmentation.Condition{Field: "days_since_last_order", Op: "gte", Numbers: []float64{365}}
	lapsedCampain = Campaign{BaseLanguage: "tr", Languages: []string{"tr", "en"},
		Audience: Audience{Match: "all", Conditions: []segmentation.Condition{
			{Field: "bought_category", Op: "exists", Category: "electronics"}, {Field: "days_since_last_order", Op: "gte", Numbers: []float64{90}}}}}
)

// Real output of the earlier prompt (campaign 6): generic win-back copy for every group.
var lapsedBase = Draft{Language: "tr", Content: Content{SMSEnabled: true, EmailEnabled: true,
	SMSBody:      "{{first_name}}, elektronik ürünlerimize geri dönmeniz için sizi özledik. Ürün gamımıza göz atmak ister misiniz?",
	EmailSubject: "Sizi özledik, elektronik dünyasına tekrar hoş geldiniz",
	EmailBody:    "Merhaba {{first_name}},\n\nBir süredir sizi göremedik ve sizi özledik. Daha önce elektronik ürünlerimizi tercih ettiğiniz için teşekkür ederiz.\n\nElektronik ürün gamımıza yeniden göz atmak isterseniz, ihtiyacınıza uygun ürünleri bulmanız için size yardımcı olmaktan memnuniyet duyarız.\n\nSizi tekrar aramızda görmek için sabırsızlanıyoruz.\n\nSevgilerle,\nThe team"}}

func lapsedGroups(en map[int]Version) []Group {
	tr := Version{SMSBody: lapsedBase.SMSBody, EmailSubject: lapsedBase.EmailSubject, EmailBody: lapsedBase.EmailBody}
	mk := func(pos int, name string, conds ...segmentation.Condition) Group {
		return Group{Position: pos, Name: name, Match: "all", Conditions: conds, Status: "generated", remainder: pos == 0,
			Languages: map[string]int{"tr": 10, "en": 5}, Content: map[string]Version{"en": en[pos], "tr": tr}, Issues: []Issue{}}
	}
	return []Group{
		mk(1, "Recent cart intent", cartRule),
		mk(2, "High-value lapsed buyers", spendRule, noCartRule),
		mk(3, "Long-dormant electronics buyers", dormantRule),
		mk(0, "Everyone else"),
	}
}

func warnCodes(g Group) []string {
	var out []string
	for _, i := range g.Issues {
		if i.Blocking {
			continue
		}
		out = append(out, i.Code+"/"+i.Language)
	}
	return out
}

func TestCheckGroupsFlagsGenericCopy(t *testing.T) {
	gs := lapsedGroups(map[int]Version{
		1: {SMSBody: "{{first_name}}, the electronics waiting in your cart are still here. Want to take another look?", EmailSubject: "Your cart is still waiting for you",
			EmailBody: "Hi {{first_name}},\n\nWe haven't seen you in a while, and we miss you. Thank you for your interest in our electronics.\n\nIf you'd like to take another look at the products waiting in your cart, we're happy to help you find the right items for your needs.\n\nWe look forward to welcoming you back.\n\nWarm regards,\nThe team"},
		2: {SMSBody: "{{first_name}}, we miss you and would love to see you back in our electronics range. Would you like to take a look?", EmailSubject: "We miss you, welcome back to our electronics range",
			EmailBody: "Hello {{first_name}},\n\nIt has been a while since we last saw you, and we miss you. Thank you for choosing our electronics in the past.\n\nIf you would like to browse our electronics range again, we are happy to help you find products that suit your needs.\n\nWe look forward to welcoming you back.\n\nWarm regards,\nThe team"},
		3: {SMSBody: "{{first_name}}, we miss you and our electronics range. Would you like to take a look at what's new?", EmailSubject: "We miss you, welcome back to electronics",
			EmailBody: "Hello {{first_name}},\n\nIt has been a while since we last saw you, and we miss you. Thank you for choosing our electronics in the past.\n\nIf you would like to take another look at our electronics range, we are happy to help you find products that suit your needs.\n\nWe look forward to welcoming you back.\n\nWarm regards,\nThe team"},
		0: {SMSBody: "{{first_name}}, we miss you! Would you like to browse our electronics range again?", EmailSubject: "We miss you, welcome back to the world of electronics",
			EmailBody: "Hello {{first_name}},\n\nWe haven't seen you in a while and we miss you. Thank you for choosing our electronics products before.\n\nIf you'd like to browse our electronics range again, we'd be happy to help you find products that suit your needs.\n\nWe look forward to seeing you again.\n\nWith love,\nThe team"},
	})
	checkGroups(lapsedCampain, lapsedBase, true, gs)
	want := map[string][]string{
		// The English cart message is genuinely different; the Turkish copies are the base message.
		"Recent cart intent":              {"group_like_base/tr"},
		"High-value lapsed buyers":        {"group_like_base/tr", "group_similar/tr"},
		"Long-dormant electronics buyers": {"group_like_base/tr", "group_similar/en", "group_similar/tr", "group_similar/tr"},
		"Everyone else":                   {"group_similar/en", "group_similar/tr", "group_similar/tr", "group_similar/tr"},
	}
	for _, g := range gs {
		got := warnCodes(g)
		slices.Sort(got)
		w := want[g.Name]
		slices.Sort(w)
		if !slices.Equal(got, w) {
			t.Errorf("%s: %v, want %v", g.Name, got, w)
		}
	}
}

func TestCheckGroupsAcceptsDistinctMessages(t *testing.T) {
	gs := lapsedGroups(map[int]Version{
		1: {SMSBody: "{{first_name}}, your saved electronics are still in your cart. Finish your order in a minute.", EmailSubject: "Your picks are still in your cart",
			EmailBody: "Hi {{first_name}},\n\nThe items you added are still saved in your cart. Complete your order whenever you're ready."},
		2: {SMSBody: "{{first_name}}, thank you for being one of our most valued electronics customers. A personal look at this season's flagship devices awaits.", EmailSubject: "For our most valued customers",
			EmailBody: "Dear {{first_name}},\n\nYou have been one of our most loyal customers. We've curated this season's flagship devices for you to explore."},
		3: {SMSBody: "{{first_name}}, a lot has changed since your last visit: discover the newest laptops, phones and audio gear.", EmailSubject: "See what's new since your last visit",
			EmailBody: "Hi {{first_name}},\n\nIt has been over a year. Our range has grown with new laptops, phones and audio gear: discover what's new."},
		0: {SMSBody: "{{first_name}}, browse our electronics range today.", EmailSubject: "Our electronics range",
			EmailBody: "Hi {{first_name}},\n\nBrowse our electronics range today."},
	})
	for i := range gs { // Turkish copies are distinct too
		gs[i].Content["tr"] = Version{SMSBody: gs[i].Content["en"].SMSBody + " (tr)", EmailSubject: "x", EmailBody: "y"}
		delete(gs[i].Languages, "tr")
	}
	checkGroups(lapsedCampain, lapsedBase, true, gs)
	for _, g := range gs {
		if c := warnCodes(g); len(c) != 0 {
			t.Errorf("%s: unexpected warnings %v", g.Name, c)
		}
	}
}

func TestCheckGroupsCartClaimsAndDifferentiation(t *testing.T) {
	cart := Version{SMSBody: "{{first_name}}, your cart is waiting.", EmailSubject: "Sepetiniz", EmailBody: "Your basket."}
	no := false
	gs := []Group{
		{Position: 1, Name: "Cart", Match: "all", Conditions: []segmentation.Condition{cartRule}, Status: "generated", Languages: map[string]int{"en": 1}, Content: map[string]Version{"en": cart}},
		{Position: 2, Name: "Big spenders", Match: "all", Conditions: []segmentation.Condition{spendRule}, Status: "generated", Differentiated: &no,
			Languages: map[string]int{"en": 1}, Content: map[string]Version{"en": {SMSBody: "Hi, your cart.", EmailSubject: "a", EmailBody: "b"}}},
		{Position: 0, Name: "Everyone else", Match: "all", Status: "generated", remainder: true, Differentiated: &no,
			Languages: map[string]int{"en": 1}, Content: map[string]Version{"en": {SMSBody: "Sepetinizde ürün var.", EmailSubject: "c", EmailBody: "d"}}},
	}
	checkGroups(Campaign{Audience: Audience{Match: "all"}}, Draft{}, false, gs)
	if c := warnCodes(gs[0]); len(c) != 0 {
		t.Errorf("the cart group may mention the cart: %v", c)
	}
	if c := warnCodes(gs[1]); !slices.Contains(c, "group_claim_cart/en") || !slices.Contains(c, "group_not_distinct/") {
		t.Errorf("spend group: %v", c)
	}
	// The remainder never inherits a rule group's guarantee; "not distinct" is expected there.
	if c := warnCodes(gs[2]); !slices.Equal(c, []string{"group_claim_cart/en"}) {
		t.Errorf("remainder: %v", c)
	}
	// An audience-wide cart rule makes cart wording safe for everyone.
	any := segmentation.Condition{Field: "days_since_last_cart", Op: "lte", Numbers: []float64{30}}
	if !guaranteesCart("all", []segmentation.Condition{any, spendRule}) || guaranteesCart("any", []segmentation.Condition{any, spendRule}) {
		t.Error("cart guarantee: all-match keeps it, any-match with other rules does not")
	}
}

func TestGroupRequestsGroundTheModel(t *testing.T) {
	now := time.Now()
	c := lapsedCampain
	c.Brief = "Win back customers who bought electronics before."
	c.Audience.ConditionText = []string{"bought electronics", "last order more than 90 days ago"}
	gs := lapsedGroups(map[int]Version{})
	for i := range gs {
		gs[i].ConditionText = []string{strings.ToLower(gs[i].Name) + " rule"}
		gs[i].Angle = "angle of " + gs[i].Name
		gs[i].Size = 10
	}
	old := now.AddDate(0, 0, -400)
	members := map[int][]segmentation.Member{3: {{ID: 1, OrderCount: 2, TotalSpent: 900, LastOrderAt: &old, LastActiveAt: old, CreatedAt: old, Language: "tr", City: "Izmir"}}}
	reqs := groupRequests(c, lapsedBase.Content, gs, members, now)

	dormant := reqs[3]
	if len(dormant.Excludes) != 2 || !strings.Contains(dormant.Excludes[0], "Recent cart intent") || !strings.Contains(dormant.Excludes[1], "High-value") {
		t.Errorf("a later group excludes the earlier ones: %v", dormant.Excludes)
	}
	if len(dormant.Others) != 3 || !strings.Contains(strings.Join(dormant.Others, "|"), "angle of Recent cart intent") {
		t.Errorf("others %v", dormant.Others)
	}
	if !strings.Contains(dormant.Profile, "Reachable customers: 1") || dormant.Remainder || dormant.Angle != "angle of Long-dormant electronics buyers" {
		t.Errorf("dormant request %+v", dormant)
	}
	if first := reqs[1]; len(first.Excludes) != 0 {
		t.Errorf("the first group excludes nobody: %v", first.Excludes)
	}
	rest := reqs[0]
	if !rest.Remainder || len(rest.Excludes) != 3 {
		t.Errorf("everyone else excludes every rule group: %+v", rest.Excludes)
	}

	p := groupContentPrompt(dormant)
	for _, want := range []string{"Guaranteed for every member:\n- bought electronics\n- last order more than 90 days ago\n- long-dormant electronics buyers rule",
		"Not in this group (earlier groups; their behavior does not apply here):\n- “Recent cart intent”", "Typical members (tendencies, not facts):\nReachable customers: 1",
		"Planned angle: angle of Long-dormant", "Other groups in this campaign (your message must differ from theirs):"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q:\n%s", want, p)
		}
	}
	if p := groupContentPrompt(rest); !strings.Contains(p, "Everyone else (customers matching none of the groups)") || !strings.Contains(p, "no behavioral assumptions") {
		t.Errorf("remainder prompt:\n%s", p)
	}
}

func TestParseGroupContent(t *testing.T) {
	raw := `{"rationale":"Builds on cart activity.","differentiated":true,"versions":[{"language":"en","sms_body":"a","email_subject":"b","email_body":"c"}]}`
	r, err := parseGroupContent(raw, []string{"en"})
	if err != nil || r.Rationale != "Builds on cart activity." || !r.Differentiated || r.Versions["en"].SMSBody != "a" {
		t.Fatalf("%+v, %v", r, err)
	}
	if _, err := parseGroupContent(`{"rationale":"x","differentiated":true,"versions":[]}`, []string{"en"}); err == nil {
		t.Error("a missing language must be rejected")
	}
}

func TestCheckGroupsSameAngle(t *testing.T) {
	gs := []Group{
		{Position: 1, Name: "Spend 3000+", Angle: "Invite them to explore the new collection.", Status: "proposed"},
		{Position: 2, Name: "Spend 2500-3000", Angle: "Invite them to explore the new collection.", Status: "proposed"},
		{Position: 3, Name: "New sign-ups", Angle: "Welcome them and suggest a first order.", Status: "proposed"},
		{Position: 0, Name: "Everyone else", Status: "proposed", remainder: true},
	}
	checkGroups(Campaign{}, Draft{}, false, gs)
	if c := warnCodes(gs[1]); !slices.Equal(c, []string{"group_same_angle/"}) {
		t.Errorf("same angle: %v", c)
	}
	for _, i := range []int{0, 2, 3} {
		if c := warnCodes(gs[i]); len(c) != 0 {
			t.Errorf("%s: %v", gs[i].Name, c)
		}
	}
}
