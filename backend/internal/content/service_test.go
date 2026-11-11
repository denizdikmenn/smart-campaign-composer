package content

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smart-campaign-composer/backend/internal/db"
	"github.com/smart-campaign-composer/backend/internal/dbtest"
	"github.com/smart-campaign-composer/backend/internal/llm"
	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

// scripted streams a fixed draft reply (or fails after streaming part of
// it); every other call is the deterministic Mock.
type scripted struct {
	Mock
	reply string
	err   error
	seen  *Request
}

func (scripted) Source() string { return "claude" }
func (g scripted) Generate(_ context.Context, req Request, onDelta func(string)) (Result, error) {
	if g.seen != nil {
		*g.seen = req
	}
	for _, c := range chunk(g.reply, 9) {
		onDelta(c)
	}
	res := Result{Usage: Usage{Raw: g.reply, InputTokens: 10, OutputTokens: 20, Model: "claude-haiku-5-5"}}
	if g.err != nil {
		return res, g.err
	}
	r, err := parseReply(g.reply)
	res.Reply = r
	return res, err
}

const brief = "Send a 30% discount reminder to customers in Istanbul."

var istanbul = Audience{Name: "Istanbul", Match: "all", Conditions: []segmentation.Condition{{Field: "city", Op: "in", Strings: []string{"Istanbul"}}}}

// fixture: 40 Istanbul customers, all with email consent, even ids with SMS.
// Languages: every 5th 'en', ids ending in 3 'de' (falls back to en), rest 'tr'.
// ids 1-10: 4 orders; 11-20: 1 order; 21-30: recent cart only; 31-40: nothing.
// Customer 40 never ordered and has an unusually long name.
func fixture(t *testing.T) (context.Context, *pgxpool.Pool, *segmentation.Service) {
	t.Helper()
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO customers (id, first_name, last_name, email, phone, city, language, email_consent, sms_consent)
		SELECT g, CASE WHEN g = 40 THEN 'Muhammed Emin' ELSE 'Ali' END, 'L', 'c' || g || '@example.com',
		       '+9053200000' || lpad(g::text, 2, '0'), 'Istanbul',
		       CASE WHEN g % 5 = 0 THEN 'en' WHEN g % 10 = 3 THEN 'de' ELSE 'tr' END, true, g % 2 = 0
		FROM generate_series(1, 40) g;
		INSERT INTO orders (customer_id, category, total_amount, placed_at)
		SELECT c, 'fashion', 100 + c, now() - interval '5 days' FROM generate_series(1, 10) c, generate_series(1, 4);
		INSERT INTO orders (customer_id, category, total_amount, placed_at)
		SELECT c, 'books', 50, now() - interval '200 days' FROM generate_series(11, 20) c;
		INSERT INTO cart_additions (customer_id, category, added_at)
		SELECT c, 'electronics', now() - interval '3 days' FROM generate_series(21, 30) c;`); err != nil {
		t.Fatal(err)
	}
	return ctx, pool, segmentation.NewService(pool, segmentation.Mock{}, slog.New(slog.DiscardHandler))
}

func newSvc(pool *pgxpool.Pool, g Generator, seg *segmentation.Service) *Service {
	return NewService(pool, g, seg, slog.New(slog.DiscardHandler))
}

func TestDraftsVariantsAndApproval(t *testing.T) {
	ctx, pool, seg := fixture(t)
	svc := newSvc(pool, scripted{reply: goodReply}, seg)

	a := istanbul
	a.Counts.Total = 999 // client-sent counts are ignored
	v, err := svc.Create(ctx, brief, a)
	if err != nil {
		t.Fatal(err)
	}
	id, c := v.Campaign.ID, v.Campaign
	if c.Audience.Counts.Total != 40 || c.BaseLanguage != "tr" || strings.Join(c.Languages, ",") != "tr,en" || c.Audience.Languages["en"] != 12 {
		t.Fatalf("audience %+v languages %v", c.Audience, c.Languages)
	}

	var events []Event
	d1, err := svc.Generate(ctx, id, GenerateInput{}, func(e Event) { events = append(events, e) })
	if err != nil || d1.Version != 1 || d1.Origin != "initial" || d1.Language != "tr" {
		t.Fatalf("draft 1: %+v, %v", d1, err)
	}
	if last := events[len(events)-1]; last.Type != "localizing" || last.Languages[0] != "en" {
		t.Fatalf("expected a localizing event last: %+v", last)
	}
	if st := d1.Localization; len(st) != 2 || st[1].Status != "ok" || d1.Translations["en"].SMSBody == "" {
		t.Fatalf("draft 1 localization %+v", st)
	}
	if !strings.Contains(d1.Translations["en"].SMSBody, "30%") {
		t.Fatalf("translation lost the offer: %q", d1.Translations["en"].SMSBody)
	}

	// Regenerating, creating and duplicating never touch other drafts.
	d2, err := svc.Generate(ctx, id, GenerateInput{FromVersion: 1, Feedback: "shorter"}, func(Event) {})
	if err != nil || d2.Origin != "regenerate" || *d2.ParentVersion != 1 {
		t.Fatalf("draft 2: %+v, %v", d2, err)
	}
	d3, err := svc.CreateDraft(ctx, id, 0)
	if err != nil || d3.Origin != "manual" || d3.SMSBody != "" || !Blocking(d3.Issues) {
		t.Fatalf("blank draft: %+v, %v", d3, err)
	}
	d4, err := svc.CreateDraft(ctx, id, 2)
	if err != nil || d4.Origin != "duplicate" || d4.SMSBody != d2.SMSBody || d4.Translations["en"] != d2.Translations["en"] {
		t.Fatalf("duplicate: %+v, %v", d4, err)
	}
	v, _ = svc.Get(ctx, id)
	if len(v.Drafts) != 4 || v.Drafts[0].SMSBody != d1.SMSBody || v.Drafts[0].UpdatedAt != d1.UpdatedAt {
		t.Fatalf("drafts changed: %d", len(v.Drafts))
	}

	t.Run("edits make translations stale until updated", func(t *testing.T) {
		e := DraftEdit{Content: d4.Content, Translations: d4.Translations}
		e.SMSBody = "Merhaba {{first_name}}, %30 indirim seni bekliyor."
		d, err := svc.SaveDraft(ctx, id, 4, e)
		if err != nil || d.Localization[1].Status != "stale" || Blocking(d.Issues) {
			t.Fatalf("after edit: %+v %+v, %v", d.Localization, d.Issues, err)
		}
		d, err = svc.Localize(ctx, id, 4)
		if err != nil || d.Localization[1].Status != "ok" {
			t.Fatalf("after localize: %+v, %v", d.Localization, err)
		}
		// An English SMS over the limit blocks, like the base text would.
		e = DraftEdit{Content: d.Content, Translations: d.Translations}
		en := e.Translations["en"]
		en.SMSBody = strings.Repeat("x", 161)
		e.Translations["en"] = en
		d, _ = svc.SaveDraft(ctx, id, 4, e)
		if !Blocking(d.Issues) || d.Issues[0].Language != "en" {
			t.Fatalf("long english sms: %+v", d.Issues)
		}
		en.SMSBody = d2.Translations["en"].SMSBody
		e.Translations["en"] = en
		if _, err := svc.SaveDraft(ctx, id, 4, e); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("variants are explicit, 2-3, replaceable", func(t *testing.T) {
		for _, bad := range [][]int{{1}, {1, 2, 3, 4}, {1, 1}} {
			if err := svc.SetVariants(ctx, id, bad); !errors.Is(err, ErrInvalid) {
				t.Fatalf("%v: %v", bad, err)
			}
		}
		if err := svc.SetVariants(ctx, id, []int{1, 9}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing draft: %v", err)
		}
		if err := svc.SetVariants(ctx, id, []int{1, 2, 4}); err != nil {
			t.Fatal(err)
		}
		if err := svc.SetVariants(ctx, id, []int{4, 1}); err != nil { // replace + remove
			t.Fatal(err)
		}
		v, _ := svc.Get(ctx, id)
		vs := v.Campaign.Variants
		if len(vs) != 2 || vs[0].Label != "A" || vs[0].DraftVersion != 4 || vs[1].Label != "B" || vs[1].DraftVersion != 1 {
			t.Fatalf("variants %+v", vs)
		}
		if err := svc.SetPersonalization(ctx, id, ModeGroups, 1); !errors.Is(err, ErrInvalid) {
			t.Fatalf("groups with A/B: %v", err)
		}
	})

	t.Run("candidate sees the active variants", func(t *testing.T) {
		var seen Request
		cand, err := newSvc(pool, scripted{reply: goodReply, seen: &seen}, seg).Generate(ctx, id, GenerateInput{FromVersion: 4, Intent: "candidate"}, func(Event) {})
		if err != nil || cand.Origin != "candidate" || len(seen.Others) != 2 || seen.Intent != "candidate" {
			t.Fatalf("candidate %+v, others %d, %v", cand, len(seen.Others), err)
		}
		v, _ := svc.Get(ctx, id)
		if len(v.Campaign.Variants) != 2 {
			t.Fatal("a candidate must not become a variant by itself")
		}
	})

	t.Run("failed or interrupted generation keeps drafts unchanged", func(t *testing.T) {
		before, _ := svc.Get(ctx, id)
		for _, gerr := range []error{ErrInterrupted, context.Canceled, llm.ErrBadReply} {
			if _, err := newSvc(pool, scripted{reply: goodReply[:40], err: gerr}, seg).Generate(ctx, id, GenerateInput{FromVersion: 1}, func(Event) {}); !errors.Is(err, gerr) {
				t.Fatalf("got %v", err)
			}
		}
		after, _ := svc.Get(ctx, id)
		if len(after.Drafts) != len(before.Drafts) {
			t.Fatalf("drafts %d -> %d", len(before.Drafts), len(after.Drafts))
		}
	})

	t.Run("review splits the audience evenly and stably", func(t *testing.T) {
		rev, err := svc.Review(ctx, id, 0)
		if err != nil || !rev.Ready {
			t.Fatalf("review %+v, %v", rev.Issues, err)
		}
		p := rev.Plan
		if p.Recipients != 40 || p.Variants[0].Recipients+p.Variants[1].Recipients != 40 || p.ByChannel["sms"] != 20 || p.ByChannel["email"] != 40 {
			t.Fatalf("plan %+v", p)
		}
		if p.ByLanguage["en"] != 12+4 || p.ByLanguage["tr"] != 28+16 { // de -> en (de ids are odd: no SMS); email + sms
			t.Fatalf("languages %v", p.ByLanguage)
		}
	})

	t.Run("approval freezes everything", func(t *testing.T) {
		v, err := svc.Approve(ctx, id, 0)
		if err != nil {
			t.Fatal(err)
		}
		var snap Snapshot
		if err := json.Unmarshal(v.Campaign.Snapshot, &snap); err != nil || len(snap.Variants) != 2 || snap.Variants[0].Draft.Version != 4 || snap.Plan.Recipients != 40 {
			t.Fatalf("snapshot %+v, %v", snap, err)
		}
		if *v.Campaign.ApprovedVersion != 4 {
			t.Fatalf("approved version %d", *v.Campaign.ApprovedVersion)
		}
		checks := map[string]error{
			"save":   func() error { _, err := svc.SaveDraft(ctx, id, 1, DraftEdit{Content: d1.Content}); return err }(),
			"create": func() error { _, err := svc.CreateDraft(ctx, id, 1); return err }(),
			"generate": func() error {
				_, err := svc.Generate(ctx, id, GenerateInput{FromVersion: 1}, func(Event) {})
				return err
			}(),
			"variants":  svc.SetVariants(ctx, id, []int{}),
			"select":    svc.SelectDraft(ctx, id, 2),
			"mode":      svc.SetPersonalization(ctx, id, ModeStandard, 0),
			"approve":   func() error { _, err := svc.Approve(ctx, id, 1); return err }(),
			"translate": func() error { _, err := svc.Localize(ctx, id, 3); return err }(),
		}
		for name, err := range checks {
			if !errors.Is(err, ErrApproved) {
				t.Errorf("%s after approval: %v", name, err)
			}
		}
	})
}

func TestStandardGate(t *testing.T) {
	ctx, pool, seg := fixture(t)
	svc := newSvc(pool, Mock{}, seg)
	v, _ := svc.Create(ctx, brief, istanbul)
	id := v.Campaign.ID
	d, err := svc.Generate(ctx, id, GenerateInput{}, func(Event) {})
	if err != nil {
		t.Fatal(err)
	}
	e := DraftEdit{Content: d.Content, Translations: d.Translations}
	e.EmailBody = "Merhaba {{first_name}},\n\nİlk siparişine %30 indirim!\n\nEkibimiz"
	if _, err := svc.SaveDraft(ctx, id, 1, e); err != nil {
		t.Fatal(err)
	}
	rev, err := svc.Review(ctx, id, 1)
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, i := range rev.Issues {
		codes = append(codes, i.Code)
	}
	if !rev.Ready || !strings.Contains(strings.Join(codes, ","), "first_purchase") {
		t.Fatalf("want a first-purchase warning only: %+v", rev.Issues)
	}
	// Missing translation blocks approval.
	if _, err := svc.CreateDraft(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	e.Translations = map[string]Version{"en": {}}
	if _, err := svc.SaveDraft(ctx, id, 1, e); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Approve(ctx, id, 1)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("missing translation must block: %v", err)
	}
}

// badGroups proposes one usable and one invalid group.
type badGroups struct{ Mock }

func (badGroups) ProposeGroups(context.Context, GroupsRequest) (GroupsResult, error) {
	return GroupsResult{Outcome: "groups", Groups: []ProposedGroup{
		{Name: "Buyers", Description: "Ordered at least once.", Match: "all", Conditions: []segmentation.Condition{{Field: "order_count", Op: "gte", Numbers: []float64{1}}}},
		{Name: "Shoe lovers", Description: "Like shoes.", Match: "all", Conditions: []segmentation.Condition{{Field: "favourite_product", Op: "in", Strings: []string{"shoes"}}}},
		{Name: "Nobody", Description: "Spent a fortune.", Match: "all", Conditions: []segmentation.Condition{{Field: "total_spent", Op: "gte", Numbers: []float64{9e6}}}},
	}}, nil
}

func TestBehavioralGroups(t *testing.T) {
	ctx, pool, seg := fixture(t)
	svc := newSvc(pool, Mock{}, seg)
	v, _ := svc.Create(ctx, brief, istanbul)
	id := v.Campaign.ID
	if _, err := svc.Generate(ctx, id, GenerateInput{}, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ProposeGroups(ctx, id); !errors.Is(err, ErrInvalid) {
		t.Fatalf("groups need the mode: %v", err)
	}
	if err := svc.SetPersonalization(ctx, id, ModeGroups, 1); err != nil {
		t.Fatal(err)
	}
	note, err := svc.ProposeGroups(ctx, id)
	if err != nil || note.Outcome != "groups" {
		t.Fatalf("propose %+v, %v", note, err)
	}
	v, _ = svc.Get(ctx, id)
	sizes, total := map[string]int{}, 0
	for _, g := range v.Groups {
		sizes[g.Name] = g.Size
		total += g.Size
	}
	// Mock rules: >=3 orders, >=1 order, cart in 30 days; the rest is the remainder.
	if total != 40 || sizes["Repeat buyers"] != 10 || sizes["Occasional buyers"] != 10 || sizes["Recent browsers"] != 10 || sizes["Everyone else"] != 10 {
		t.Fatalf("sizes %v", sizes)
	}
	ex, err := svc.GroupExamples(ctx, id, 1)
	if err != nil || len(ex) != 1 || ex[0].Member.OrderCount != 4 {
		t.Fatalf("examples %+v, %v", ex, err)
	}

	est, _ := svc.GroupsEstimate(ctx, id)
	if est.Calls != 4 {
		t.Fatalf("estimate %+v", est)
	}
	if err := svc.GenerateGroupContent(ctx, id); err != nil {
		t.Fatal(err)
	}
	svc.Wait("groups", id)
	v, _ = svc.Get(ctx, id)
	for _, g := range v.Groups {
		if g.Status != "generated" || len(g.Content) != len(g.Languages) {
			t.Fatalf("group %s: %s, content %d languages %v", g.Name, g.Status, len(g.Content), g.Languages)
		}
		for l := range g.Content {
			if g.Languages[l] == 0 {
				t.Fatalf("group %s has content for absent language %s", g.Name, l)
			}
		}
	}
	rev, err := svc.Review(ctx, id, 0)
	if err != nil || !rev.Ready || rev.Plan.BySource["group"] != rev.Plan.Messages || len(rev.Plan.Groups) != 4 {
		t.Fatalf("review %+v %+v, %v", rev.Issues, rev.Plan, err)
	}
	if err := svc.SetVariants(ctx, id, []int{1, 1}); !errors.Is(err, ErrInvalid) {
		t.Fatal("variants must be rejected in groups mode")
	}

	t.Run("invalid and empty proposed groups are dropped", func(t *testing.T) {
		note, err := newSvc(pool, badGroups{}, seg).ProposeGroups(ctx, id)
		if err != nil || len(note.Warnings) != 2 {
			t.Fatalf("note %+v, %v", note, err)
		}
		v, _ := svc.Get(ctx, id)
		if len(v.Groups) != 2 || v.Groups[0].Size != 20 || v.Groups[1].Size != 20 || v.Groups[1].Position != 0 {
			t.Fatalf("groups %+v", v.Groups)
		}
	})
}

// counting personalizes like Mock but records peak concurrency; customers
// with an id in invent get an invented offer.
type counting struct {
	Mock
	cur, peak, calls *int32
	invent           map[string]bool
}

func (g counting) Personalize(ctx context.Context, req PersonalizeRequest) (PersonalResult, error) {
	n := atomic.AddInt32(g.cur, 1)
	defer atomic.AddInt32(g.cur, -1)
	atomic.AddInt32(g.calls, 1)
	for {
		p := atomic.LoadInt32(g.peak)
		if n <= p || atomic.CompareAndSwapInt32(g.peak, p, n) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	res, err := g.Mock.Personalize(ctx, req)
	if g.invent[req.Customer.FirstName] {
		res.Version.EmailBody += " Use code VIP50 for 50% off."
	}
	return res, err
}

func TestIndividualPersonalization(t *testing.T) {
	ctx, pool, seg := fixture(t)
	if _, err := pool.Exec(ctx, `UPDATE customers SET first_name = 'Zeynep' WHERE id = 12`); err != nil {
		t.Fatal(err)
	}
	var cur, peak, calls int32
	gen := counting{cur: &cur, peak: &peak, calls: &calls, invent: map[string]bool{"Zeynep": true}}
	svc := newSvc(pool, gen, seg)
	v, _ := svc.Create(ctx, brief, istanbul)
	id := v.Campaign.ID
	if _, err := svc.Generate(ctx, id, GenerateInput{}, func(Event) {}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPersonalization(ctx, id, ModeIndividual, 1); err != nil {
		t.Fatal(err)
	}
	if err := svc.ApplyIndividual(ctx, id); !errors.Is(err, ErrInvalid) {
		t.Fatalf("apply needs a sample first: %v", err)
	}

	// No history: fallback without a model call.
	res, err := svc.Sample(ctx, id, 35)
	if err != nil || res.Message.Status != "fallback" || res.Input != nil || calls != 0 {
		t.Fatalf("fallback sample %+v, calls %d, %v", res, calls, err)
	}
	res, err = svc.Sample(ctx, id, 1)
	if err != nil || res.Message.Status != "generated" || calls != 1 {
		t.Fatalf("sample %+v, %v", res, err)
	}
	if res.Remaining != 29 || res.Fallbacks != 10 || res.Estimate.Calls != 29 {
		t.Fatalf("estimate: remaining %d fallbacks %d %+v", res.Remaining, res.Fallbacks, res.Estimate)
	}
	// Only the allowed fields are sent: no email, phone or last name.
	raw, _ := json.Marshal(res.Input)
	for _, k := range []string{"email", "phone", "last_name", "@example.com", "+90"} {
		if strings.Contains(string(raw), k) {
			t.Fatalf("personal input leaks %q: %s", k, raw)
		}
	}
	if res.Input.OrderCount != 4 || len(res.Input.RecentOrders) != 4 || res.Input.RecentOrders[0].Category != "fashion" {
		t.Fatalf("input %+v", res.Input)
	}

	if err := svc.ApplyIndividual(ctx, id); err != nil {
		t.Fatal(err)
	}
	svc.Wait("individual", id)
	v, _ = svc.Get(ctx, id)
	in := v.Individual
	if in.Generated != 29 || in.Failed != 1 || in.Fallback != 10 || in.Pending != 0 || !in.Applied {
		t.Fatalf("summary %+v", in)
	}
	if calls != 30 || peak > int32(svc.individualWorkers) {
		t.Fatalf("calls %d (sample reused), peak concurrency %d", calls, peak)
	}
	var reason string
	_ = pool.QueryRow(ctx, `SELECT reason FROM personalized_messages WHERE campaign_id = $1 AND customer_id = 12`, id).Scan(&reason)
	if !strings.Contains(reason, "not in the brief") {
		t.Fatalf("invented offer must be rejected: %q", reason)
	}
	rev, err := svc.Review(ctx, id, 0)
	if err != nil || !rev.Ready {
		t.Fatalf("review %+v, %v", rev.Issues, err)
	}
	if rev.Plan.BySource["individual"] == 0 || rev.Plan.BySource["fallback"] == 0 {
		t.Fatalf("sources %v", rev.Plan.BySource)
	}
	var runs int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM content_runs WHERE campaign_id = $1 AND kind = 'individual'`, id).Scan(&runs)
	if runs != 30 {
		t.Fatalf("traced individual calls: %d", runs)
	}
}
