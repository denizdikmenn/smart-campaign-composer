package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

// ErrNotApproved is returned when approved content is requested for a draft campaign.
var ErrNotApproved = errors.New("the campaign must be approved before it can be sent")

// approvedResolver builds the resolver from the frozen approval snapshot, so
// what is sent is exactly what was approved, whatever happened to the drafts
// table since. The returned view carries the snapshot's audience, variants
// and groups (what plan needs). Individual messages are read from
// personalized_messages, which no longer change once the campaign is approved.
func (s *Service) approvedResolver(ctx context.Context, id int64) (View, *resolver, error) {
	c, err := s.loadCampaign(ctx, s.pool, id, false)
	if err != nil {
		return View{}, nil, err
	}
	if c.Status != "approved" || len(c.Snapshot) == 0 {
		return View{}, nil, ErrNotApproved
	}
	var snap Snapshot
	if err := json.Unmarshal(c.Snapshot, &snap); err != nil {
		return View{}, nil, fmt.Errorf("approved snapshot: %w", err)
	}
	c.Personalization, c.Audience, c.Languages, c.BaseLanguage = snap.Mode, snap.Audience, snap.Languages, snap.BaseLanguage
	c.Variants = []Variant{}
	r := &resolver{campaignID: c.ID, mode: snap.Mode, groups: map[int]Group{}}
	if snap.Draft != nil {
		r.base = *snap.Draft
	}
	for i, va := range snap.Variants {
		c.Variants = append(c.Variants, Variant{Label: va.Label, Position: i + 1, DraftVersion: va.Draft.Version})
		r.variants = append(r.variants, va.Draft)
	}
	groups := make([]Group, len(snap.Groups))
	for i, g := range snap.Groups {
		g.remainder = g.Position == 0 // unexported, not in the JSON
		groups[i] = g
		r.groups[g.Position] = g
	}
	if snap.Mode == ModeIndividual {
		if r.personal, err = s.personalByCustomer(ctx, id); err != nil {
			return View{}, nil, err
		}
	}
	return View{Campaign: c, Groups: groups}, r, nil
}

// ApprovedMessages resolves the approved content for the campaign's current
// audience: membership and channel consent are re-read now, the content and
// variant/group assignment come from the approval snapshot. fn is called once
// per audience member and enabled channel; channels the member has not
// consented to come with Skip set (recorded as skipped, never sent).
func (s *Service) ApprovedMessages(ctx context.Context, id int64, fn func(Message) error) error {
	v, r, err := s.approvedResolver(ctx, id)
	if err != nil {
		return err
	}
	var groups = groupRules(v.Groups)
	if r.mode != ModeGroups {
		groups = nil
	}
	return s.aud.Members(ctx, v.Campaign.Audience.rules(), groups, func(m segmentation.Member) error {
		msgs, skipped := r.resolveAll(m)
		for _, msg := range append(msgs, skipped...) {
			if err := fn(msg); err != nil {
				return err
			}
		}
		return nil
	})
}

// approvedReview is the pre-send summary of an approved campaign: a fresh
// recipient plan (current audience and consent, approved content).
func (s *Service) approvedReview(ctx context.Context, v View) (Review, error) {
	av, r, err := s.approvedResolver(ctx, v.Campaign.ID)
	if err != nil {
		return Review{}, err
	}
	rev := Review{Ready: true, Mode: r.mode, Issues: []Issue{}, Cost: v.Cost}
	if v.Campaign.ApprovedVersion != nil && len(r.variants) == 0 {
		rev.Version = *v.Campaign.ApprovedVersion
	}
	rev.Plan, err = s.plan(ctx, av, r)
	return rev, err
}
