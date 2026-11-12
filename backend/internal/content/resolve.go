package content

import (
	"context"
	"fmt"
	"sort"

	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

// Message is the resolved content for one recipient and channel: what a
// delivery record stores. VariantPosition (1-3) and GroupPosition identify
// the experiment arm and behavioral group for monitoring.
type Message struct {
	CustomerID      int64  `json:"customer_id"`
	FirstName       string `json:"first_name"`
	Channel         string `json:"channel"` // sms | email
	VariantPosition int    `json:"variant_position,omitempty"`
	GroupPosition   *int   `json:"group_position,omitempty"`
	Language        string `json:"language"`
	Source          string `json:"source"` // template | group | individual | fallback
	Subject         string `json:"subject,omitempty"`
	Body            string `json:"body"`
	// Skip is set (and Body empty) for an enabled channel the member cannot
	// receive, e.g. "not eligible: no SMS consent". Only resolveAll returns these.
	Skip string `json:"-"`
}

// resolver turns an audience member into their messages. It is pure, so the
// same inputs always give the same messages (and variant) for a customer.
type resolver struct {
	campaignID int64
	mode       string
	base       Draft   // the selected draft (Standard, and base for groups/individual)
	variants   []Draft // A/B arms in order; empty when there is no experiment
	groups     map[int]Group
	personal   map[int64]PersonalMessage
}

// pickVersion returns d's text for want, falling back to FallbackLanguage
// and then to the draft's own language.
func pickVersion(d Draft, want string) (Version, string) {
	if v, ok := d.TextIn(want); ok {
		return v, want
	}
	if v, ok := d.TextIn(FallbackLanguage); ok {
		return v, FallbackLanguage
	}
	v, _ := d.TextIn(d.Language)
	return v, d.Language
}

func (r *resolver) channels() Content {
	if len(r.variants) > 0 {
		return r.variants[0].Content
	}
	return r.base.Content
}

func (r *resolver) resolve(m segmentation.Member) []Message {
	out, _ := r.resolveAll(m)
	return out
}

// resolveAll also returns the member's skipped channels: enabled for the
// campaign but without the member's consent.
func (r *resolver) resolveAll(m segmentation.Member) (out, skipped []Message) {
	want := ContentLanguage(m.Language)
	draft, variant := r.base, 0
	if n := len(r.variants); n > 0 {
		variant = variantFor(r.campaignID, m.ID, n)
		draft = r.variants[variant-1]
	}
	v, lang := pickVersion(draft, want)
	source := "template"
	var group *int
	switch r.mode {
	case ModeGroups:
		pos := m.Group
		group = &pos
		source = "fallback"
		if g, ok := r.groups[m.Group]; ok {
			for _, l := range []string{want, FallbackLanguage} {
				if gv, ok := g.Content[l]; ok && !gv.empty() {
					v, lang, source = gv, l, "group"
					break
				}
			}
		}
	case ModeIndividual:
		source = "fallback"
		if p, ok := r.personal[m.ID]; ok && p.Status == "generated" {
			v, lang, source = p.Version, p.Language, "individual"
		}
	}
	ch := r.channels()
	base := Message{CustomerID: m.ID, FirstName: m.FirstName, VariantPosition: variant, GroupPosition: group, Language: lang, Source: source}
	if ch.SMSEnabled {
		msg := base
		msg.Channel = "sms"
		if m.SMSConsent {
			msg.Body = Render(v.SMSBody, m.FirstName)
			out = append(out, msg)
		} else {
			msg.Skip = "not eligible: no SMS consent"
			skipped = append(skipped, msg)
		}
	}
	if ch.EmailEnabled {
		msg := base
		msg.Channel = "email"
		if m.EmailConsent {
			msg.Subject, msg.Body = Render(v.EmailSubject, m.FirstName), Render(v.EmailBody, m.FirstName)
			out = append(out, msg)
		} else {
			msg.Skip = "not eligible: no email consent"
			skipped = append(skipped, msg)
		}
	}
	return out, skipped
}

// Plan summarizes who receives what, computed by resolving every reachable
// member (the same resolution a delivery run uses).
type Plan struct {
	Recipients   int            `json:"recipients"` // reachable people receiving at least one message
	Messages     int            `json:"messages"`
	ByChannel    map[string]int `json:"by_channel"`
	ByLanguage   map[string]int `json:"by_language"`
	BySource     map[string]int `json:"by_source"`
	Variants     []Bucket       `json:"variants"`
	Groups       []Bucket       `json:"groups"`
	PriorBuyers  int            `json:"prior_buyers"`  // recipients who ordered before
	NeverOrdered int            `json:"never_ordered"` // recipients who never ordered
	Samples      []Message      `json:"samples"`       // one per variant/group × language × channel (capped)
}

// Bucket is one variant or group in the plan.
type Bucket struct {
	Position   int     `json:"position"`
	Label      string  `json:"label"`
	Recipients int     `json:"recipients"`
	SMS        int     `json:"sms"`
	Email      int     `json:"email"`
	Share      float64 `json:"share"`
	// For the consistency gate: recipients with and without earlier orders.
	PriorBuyers  int `json:"prior_buyers"`
	NeverOrdered int `json:"never_ordered"`
}

const maxPlanSamples = 12

func (s *Service) buildResolver(ctx context.Context, v View, version int) (*resolver, error) {
	c := v.Campaign
	r := &resolver{campaignID: c.ID, mode: c.Personalization, groups: map[int]Group{}}
	sel := 0
	if c.SelectedVersion != nil {
		sel = *c.SelectedVersion
	}
	if version > 0 && len(c.Variants) == 0 && c.Personalization == ModeStandard {
		sel = version
	}
	if d, ok := findDraft(v.Drafts, sel); ok {
		r.base = d
	}
	for _, va := range c.Variants {
		d, ok := findDraft(v.Drafts, va.DraftVersion)
		if !ok {
			return nil, fmt.Errorf("variant %s: draft %d missing", va.Label, va.DraftVersion)
		}
		r.variants = append(r.variants, d)
	}
	for _, g := range v.Groups {
		r.groups[g.Position] = g
	}
	if c.Personalization == ModeIndividual {
		var err error
		if r.personal, err = s.personalByCustomer(ctx, c.ID); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (s *Service) plan(ctx context.Context, v View, r *resolver) (Plan, error) {
	p := Plan{ByChannel: map[string]int{}, ByLanguage: map[string]int{}, BySource: map[string]int{}, Variants: []Bucket{}, Groups: []Bucket{}, Samples: []Message{}}
	var groupRulesList []segmentation.Alternative
	if r.mode == ModeGroups {
		groupRulesList = groupRules(v.Groups)
	}
	variants := map[int]*Bucket{}
	for i, va := range v.Campaign.Variants {
		variants[i+1] = &Bucket{Position: va.Position, Label: "Variant " + va.Label + fmt.Sprintf(" (Draft %d)", va.DraftVersion)}
	}
	groups := map[int]*Bucket{}
	for _, g := range v.Groups {
		groups[g.Position] = &Bucket{Position: g.Position, Label: g.Name}
	}
	seen := map[string]bool{}
	err := s.aud.Members(ctx, v.Campaign.Audience.rules(), groupRulesList, func(m segmentation.Member) error {
		msgs := r.resolve(m)
		if len(msgs) == 0 {
			return nil
		}
		p.Recipients++
		if m.OrderCount > 0 {
			p.PriorBuyers++
		} else {
			p.NeverOrdered++
		}
		count := func(b *Bucket) {
			if b == nil {
				return
			}
			b.Recipients++
			if m.OrderCount > 0 {
				b.PriorBuyers++
			} else {
				b.NeverOrdered++
			}
			for _, msg := range msgs {
				if msg.Channel == "sms" {
					b.SMS++
				} else {
					b.Email++
				}
			}
		}
		count(variants[msgs[0].VariantPosition])
		if msgs[0].GroupPosition != nil {
			count(groups[*msgs[0].GroupPosition])
		}
		for _, msg := range msgs {
			p.Messages++
			p.ByChannel[msg.Channel]++
			p.ByLanguage[msg.Language]++
			p.BySource[msg.Source]++
			key := fmt.Sprintf("%d/%v/%s/%s/%s", msg.VariantPosition, msg.GroupPosition != nil && *msg.GroupPosition >= 0, groupKey(msg), msg.Language, msg.Channel)
			if !seen[key] && len(p.Samples) < maxPlanSamples {
				seen[key] = true
				p.Samples = append(p.Samples, msg)
			}
		}
		return nil
	})
	if err != nil {
		return p, err
	}
	for _, b := range variants {
		b.Share = share(b.Recipients, p.Recipients)
		p.Variants = append(p.Variants, *b)
	}
	for _, b := range groups {
		b.Share = share(b.Recipients, p.Recipients)
		p.Groups = append(p.Groups, *b)
	}
	sort.Slice(p.Variants, func(i, j int) bool { return p.Variants[i].Position < p.Variants[j].Position })
	sort.Slice(p.Groups, func(i, j int) bool {
		gi, gj := p.Groups[i].Position, p.Groups[j].Position
		return gi != 0 && (gj == 0 || gi < gj)
	})
	return p, nil
}

func groupKey(m Message) string {
	if m.GroupPosition == nil {
		return "-"
	}
	return fmt.Sprint(*m.GroupPosition)
}

func share(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total)
}
