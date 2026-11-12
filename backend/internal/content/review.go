package content

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
)

// Review is everything the marketer checks before approving: the gate's
// issues, who receives what, and the AI cost so far.
type Review struct {
	Ready   bool    `json:"ready"`
	Mode    string  `json:"mode"`
	Version int     `json:"version"` // draft approved in Standard mode (0 with variants)
	Issues  []Issue `json:"issues"`
	Plan    Plan    `json:"plan"`
	Cost    Cost    `json:"cost"`
}

// Review runs the consistency gate for the current configuration. version
// is the Standard draft to approve (ignored when an A/B test is active).
// For an approved campaign it returns the recipient plan of the approved
// content instead (what Send would queue now).
func (s *Service) Review(ctx context.Context, id int64, version int) (Review, error) {
	v, err := s.Get(ctx, id)
	if err != nil {
		return Review{}, err
	}
	if v.Campaign.Status == "approved" {
		return s.approvedReview(ctx, v) // the pre-send summary
	}
	return s.review(ctx, v, version)
}

// Audience–message consistency, deliberately simple keyword checks that only
// produce warnings: wording that obviously assumes a purchase history the
// audience doesn't share.
var (
	// Turkish dotted/dotless i don't case-fold to ASCII i, and \b is ASCII-only.
	firstPurchaseRe = regexp.MustCompile(`(?i)\bfirst (order|purchase)|(?:^|[^\p{L}])[iİı]lk (sipariş|alışveriş)`)
	welcomeBackRe   = regexp.MustCompile(`(?i)welcome back|we('ve| have)? missed you|tekrar hoş geldin|seni özledik`)
)

type unit struct {
	scope string
	draft Draft
}

func (s *Service) review(ctx context.Context, v View, version int) (Review, error) {
	c := v.Campaign
	rev := Review{Mode: c.Personalization, Issues: []Issue{}, Cost: v.Cost}
	add := func(i Issue) { rev.Issues = append(rev.Issues, i) }
	block := func(field, code, msg string, args ...any) {
		add(Issue{Field: field, Code: code, Blocking: true, Message: fmt.Sprintf(msg, args...)})
	}
	warn := func(field, code, msg string, args ...any) {
		add(Issue{Field: field, Code: code, Message: fmt.Sprintf(msg, args...)})
	}

	// Which drafts are in use.
	var units []unit
	switch {
	case len(c.Variants) > 0:
		if len(c.Variants) < 2 || len(c.Variants) > maxVariants {
			block("variants", "variant_count", "An A/B test needs 2 or 3 variants.")
		}
		for _, va := range c.Variants {
			d, ok := findDraft(v.Drafts, va.DraftVersion)
			if !ok {
				block("variants", "variant_missing", "Variant %s refers to a missing draft.", va.Label)
				continue
			}
			units = append(units, unit{scope: "Variant " + va.Label, draft: d})
		}
		for _, u := range units[1:] {
			if u.draft.SMSEnabled != units[0].draft.SMSEnabled || u.draft.EmailEnabled != units[0].draft.EmailEnabled {
				block("channels", "variant_channels", "All variants must use the same channels; %s differs from Variant A.", u.scope)
			}
		}
	default:
		sel := 0
		if c.SelectedVersion != nil {
			sel = *c.SelectedVersion
		}
		if version > 0 && c.Personalization == ModeStandard {
			sel = version
		}
		rev.Version = sel
		d, ok := findDraft(v.Drafts, sel)
		if !ok {
			block("draft", "no_draft", "Choose the draft to approve.")
			break
		}
		scope := fmt.Sprintf("Draft %d", d.Version)
		if c.Personalization != ModeStandard {
			scope = fmt.Sprintf("Base draft %d", d.Version)
		}
		units = append(units, unit{scope: scope, draft: d})
	}
	for _, u := range units {
		for _, i := range u.draft.Issues {
			i.Scope = u.scope
			add(i)
		}
	}

	// Personalization completeness.
	switch c.Personalization {
	case ModeGroups:
		if len(v.Groups) == 0 {
			block("groups", "no_groups", "Propose behavioral groups, or switch personalization back to Standard.")
		}
		for _, g := range v.Groups {
			switch {
			case g.Size == 0:
			case g.Status == "generating" || v.Jobs["groups"]:
				block("groups", "groups_running", "Group messages are still being written.")
			case g.Status != "generated":
				block("groups", "group_content_missing", "“%s” has no messages yet. Generate group messages.", g.Name)
			default:
				for _, i := range g.Issues {
					i.Scope = g.Name
					add(i)
				}
				if g.Stale {
					warn("groups", "group_stale", "“%s” messages were written from an earlier version of the base draft.", g.Name)
				}
			}
		}
	case ModeIndividual:
		in := v.Individual
		switch {
		case !in.Applied:
			block("individual", "not_applied", "Confirm “Personalize %d customers” to write the individual messages, or switch back to Standard.", MaxIndividual)
		case in.Running || in.Pending > 0:
			block("individual", "individual_running", "Individual messages are still being generated (%d pending).", in.Pending)
		}
		if in.Failed > 0 {
			warn("individual", "individual_failed", "%d individual messages failed or were rejected; those customers get the base message.", in.Failed)
		}
		if in.Fallback > 0 {
			warn("individual", "individual_fallback", "%d customers have too little history and get the base message.", in.Fallback)
		}
		if in.Applied {
			warn("individual", "individual_limit", "Individual messages are written for %d picked customers (limit %d in this demo); everyone else in the audience gets the base message.", in.Generated, in.Limit)
		}
		if in.Stale > 0 {
			warn("individual", "individual_stale", "%d individual messages were written from an earlier version of the base draft.", in.Stale)
		}
	}
	if rev.hasBlocking("draft", "variants") {
		return rev, nil
	}

	// Who receives what (and therefore channel reach).
	r, err := s.buildResolver(ctx, v, version)
	if err != nil {
		return rev, err
	}
	if rev.Plan, err = s.plan(ctx, v, r); err != nil {
		return rev, err
	}
	ch := r.channels()
	if ch.SMSEnabled && rev.Plan.ByChannel["sms"] == 0 {
		block("channels", "no_reach", "No one in this audience has consented to SMS. Disable the SMS channel or choose another audience.")
	}
	if ch.EmailEnabled && rev.Plan.ByChannel["email"] == 0 {
		block("channels", "no_reach", "No one in this audience has consented to email. Disable the email channel or choose another audience.")
	}

	// Audience–message consistency (warnings only). Each text is checked
	// against the recipients who actually get it.
	type target struct {
		text                string
		prior, neverOrdered int
	}
	targets := map[string]*target{}
	all := func(scope string) *target {
		if targets[scope] == nil {
			targets[scope] = &target{prior: rev.Plan.PriorBuyers, neverOrdered: rev.Plan.NeverOrdered}
		}
		return targets[scope]
	}
	for i, u := range units {
		t := all(u.scope)
		if i < len(rev.Plan.Variants) && len(c.Variants) > 0 {
			t.prior, t.neverOrdered = rev.Plan.Variants[i].PriorBuyers, rev.Plan.Variants[i].NeverOrdered
		}
		if c.Personalization == ModeGroups {
			continue // the base draft only reaches customers without group content
		}
		for _, l := range c.Languages {
			if v, ok := u.draft.TextIn(l); ok {
				t.text += "\n" + v.texts()
			}
		}
	}
	if c.Personalization == ModeGroups {
		for _, g := range v.Groups {
			t := &target{}
			for _, b := range rev.Plan.Groups {
				if b.Position == g.Position {
					t.prior, t.neverOrdered = b.PriorBuyers, b.NeverOrdered
				}
			}
			for _, v := range g.Content {
				t.text += "\n" + v.texts()
			}
			targets[g.Name] = t
		}
	}
	for scope, t := range targets {
		if firstPurchaseRe.MatchString(t.text) && t.prior > 0 {
			add(Issue{Field: "consistency", Code: "first_purchase", Scope: scope,
				Message: fmt.Sprintf("Mentions a first purchase, but %d of its recipients have ordered before.", t.prior)})
		}
		if welcomeBackRe.MatchString(t.text) && t.neverOrdered > 0 {
			add(Issue{Field: "consistency", Code: "welcome_back", Scope: scope,
				Message: fmt.Sprintf("Welcomes customers back, but %d of its recipients have never ordered.", t.neverOrdered)})
		}
	}
	rev.Ready = !Blocking(rev.Issues)
	return rev, nil
}

func (r Review) hasBlocking(fields ...string) bool {
	for _, i := range r.Issues {
		for _, f := range fields {
			if i.Blocking && i.Field == f {
				return true
			}
		}
	}
	return false
}

// Snapshot is the frozen, approved configuration.
type Snapshot struct {
	Mode         string             `json:"mode"`
	Languages    []string           `json:"languages"`
	BaseLanguage string             `json:"base_language"`
	Audience     Audience           `json:"audience"`
	Draft        *Draft             `json:"draft,omitempty"` // Standard draft, or the base for groups/individual
	Variants     []SnapshotVariant  `json:"variants,omitempty"`
	Allocation   string             `json:"allocation,omitempty"`
	Groups       []Group            `json:"groups,omitempty"`
	Individual   *IndividualSummary `json:"individual,omitempty"`
	Plan         Plan               `json:"plan"`
	Cost         Cost               `json:"cost"`
	ApprovedAt   time.Time          `json:"approved_at"`
}

type SnapshotVariant struct {
	Label string `json:"label"`
	Draft Draft  `json:"draft"`
}

// Approve re-checks the audience and runs the gate while holding the
// campaign lock, then freezes the configuration. It sends nothing.
func (s *Service) Approve(ctx context.Context, id int64, version int) (View, error) {
	var verr *ValidationError
	err := s.mutate(ctx, id, func(tx pgx.Tx, c Campaign) error {
		if s.running(jobKey("groups", id)) || s.running(jobKey("individual", id)) || s.running(jobKey("draft", id)) {
			return ErrBusy
		}
		v, err := s.Get(ctx, id)
		if err != nil {
			return err
		}
		// Recount: the audience may have changed since it was selected.
		a, err := s.recount(ctx, v.Campaign.Audience)
		if err != nil {
			return err
		}
		v.Campaign.Audience, v.Campaign.Languages, v.Campaign.BaseLanguage = a, a.LanguageList(), a.BaseLanguage()
		for i := range v.Drafts {
			v.Campaign.annotate(&v.Drafts[i])
		}
		rev, err := s.review(ctx, v, version)
		if err != nil {
			return err
		}
		if !rev.Ready {
			verr = &ValidationError{Issues: rev.Issues}
			return verr
		}
		now := time.Now().UTC()
		snap := Snapshot{Mode: v.Campaign.Personalization, Languages: v.Campaign.Languages, BaseLanguage: v.Campaign.BaseLanguage,
			Audience: a, Plan: rev.Plan, Cost: v.Cost, ApprovedAt: now}
		approved := rev.Version
		if len(v.Campaign.Variants) > 0 {
			snap.Allocation = fmt.Sprintf("equal (%d variants, deterministic per customer)", len(v.Campaign.Variants))
			for _, va := range v.Campaign.Variants {
				d, _ := findDraft(v.Drafts, va.DraftVersion)
				snap.Variants = append(snap.Variants, SnapshotVariant{Label: va.Label, Draft: d})
			}
			approved = v.Campaign.Variants[0].DraftVersion
		} else if d, ok := findDraft(v.Drafts, rev.Version); ok {
			snap.Draft = &d
		}
		switch v.Campaign.Personalization {
		case ModeGroups:
			snap.Groups = v.Groups
		case ModeIndividual:
			sum := v.Individual
			snap.Individual = &sum
		}
		sj, _ := json.Marshal(snap)
		aj, _ := json.Marshal(a)
		_, err = tx.Exec(ctx, `UPDATE campaigns SET status = 'approved', approved_version = $2, approved_at = $3, approved_snapshot = $4,
			audience = $5, selected_version = CASE WHEN $6 > 0 THEN $6 ELSE selected_version END WHERE id = $1`,
			id, approved, now, sj, aj, rev.Version)
		return err
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, id)
}

// Plan resolves the current configuration for every reachable member.
func (s *Service) RecipientPlan(ctx context.Context, id int64, version int) (Plan, error) {
	rev, err := s.Review(ctx, id, version)
	return rev.Plan, err
}
