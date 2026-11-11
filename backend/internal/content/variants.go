package content

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// variantFor deterministically assigns a customer to one of n variants
// (1-based). The same customer always gets the same variant in a campaign,
// for every channel and on every retry; the hash spreads customers evenly.
func variantFor(campaignID, customerID int64, n int) int {
	if n <= 0 {
		return 0
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%d:%d", campaignID, customerID))
	return int(binary.BigEndian.Uint64(sum[:8])%uint64(n)) + 1
}

func draftExists(ctx context.Context, tx pgx.Tx, id int64, version int) error {
	var ok bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM campaign_drafts WHERE campaign_id = $1 AND version = $2)`, id, version).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// SelectDraft chooses the draft used by a Standard campaign and as the base
// for behavioral groups and individual personalization.
func (s *Service) SelectDraft(ctx context.Context, id int64, version int) error {
	return s.mutate(ctx, id, func(tx pgx.Tx, c Campaign) error {
		if err := draftExists(ctx, tx, id, version); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE campaigns SET selected_version = $2 WHERE id = $1`, id, version)
		return err
	})
}

// SetVariants replaces the active experiment with the given drafts, in
// order A, B, C. An empty list disables A/B testing; otherwise 2-3 distinct
// existing drafts are required. Drafts themselves are never changed.
func (s *Service) SetVariants(ctx context.Context, id int64, versions []int) error {
	if len(versions) == 1 || len(versions) > maxVariants {
		return invalid("an experiment needs 2 or 3 variants")
	}
	seen := map[int]bool{}
	for _, v := range versions {
		if seen[v] {
			return invalid("a draft can be only one variant")
		}
		seen[v] = true
	}
	return s.mutate(ctx, id, func(tx pgx.Tx, c Campaign) error {
		if len(versions) > 0 && c.Personalization != ModeStandard {
			return invalid("A/B testing works with Standard content only; switch personalization back to Standard first")
		}
		for _, v := range versions {
			if err := draftExists(ctx, tx, id, v); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM campaign_variants WHERE campaign_id = $1`, id); err != nil {
			return err
		}
		for i, v := range versions {
			if _, err := tx.Exec(ctx, `INSERT INTO campaign_variants (campaign_id, position, draft_version) VALUES ($1, $2, $3)`, id, i+1, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetPersonalization switches between Standard, behavioral groups and
// individual personalization. Groups and individual messages are kept when
// switching away, and drafts are never touched. base selects the base draft.
func (s *Service) SetPersonalization(ctx context.Context, id int64, mode string, base int) error {
	if mode != ModeStandard && mode != ModeGroups && mode != ModeIndividual {
		return invalid("unknown personalization %q", mode)
	}
	if s.running(jobKey("groups", id)) || s.running(jobKey("individual", id)) {
		return ErrBusy
	}
	return s.mutate(ctx, id, func(tx pgx.Tx, c Campaign) error {
		if mode != ModeStandard && len(c.Variants) > 0 {
			return invalid("behavioral groups and individual personalization can't be combined with an A/B test; disable A/B testing first")
		}
		if base > 0 {
			if err := draftExists(ctx, tx, id, base); err != nil {
				return err
			}
		} else if mode != ModeStandard && c.SelectedVersion == nil {
			return invalid("choose the draft to personalize")
		}
		_, err := tx.Exec(ctx, `UPDATE campaigns SET personalization = $2, selected_version = COALESCE(NULLIF($3, 0), selected_version) WHERE id = $1`, id, mode, base)
		return err
	})
}
