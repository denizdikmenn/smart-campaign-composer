// Package content generates, validates and stores campaign message drafts
// (SMS + email) for a selected audience, and records the marketer's approval.
// LLM output is untrusted: it is parsed strictly and every draft is validated
// by deterministic backend rules before it can be approved.
package content

import (
	"slices"

	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

// Settings are the campaign parameters Claude infers from the brief and the
// marketer can change before regenerating.
type Settings struct {
	Tone     string `json:"tone"`
	Occasion string `json:"occasion"`
	Length   string `json:"length"`
	CTA      string `json:"cta"`
}

// Options lists the allowed values of each setting, in display order. The
// same lists feed the output schema, validation and the UI.
type Options struct {
	Tone     []string `json:"tone"`
	Occasion []string `json:"occasion"`
	Length   []string `json:"length"`
	CTA      []string `json:"cta"`
}

var SettingOptions = Options{
	Tone:     []string{"friendly", "professional", "playful", "premium", "urgent", "empathetic"},
	Occasion: []string{"welcome", "cart_recovery", "win_back", "promotion", "reminder", "announcement", "loyalty"},
	Length:   []string{"short", "balanced", "detailed"},
	CTA:      []string{"shop_now", "complete_purchase", "explore", "claim_offer", "learn_more", "come_back"},
}

// Valid reports whether every setting is one of the allowed values.
func (s Settings) Valid() bool {
	o := SettingOptions
	return slices.Contains(o.Tone, s.Tone) && slices.Contains(o.Occasion, s.Occasion) &&
		slices.Contains(o.Length, s.Length) && slices.Contains(o.CTA, s.CTA)
}

// Content is one editable version of the campaign's messages. SMS and email
// are independent templates; a disabled channel is kept but not validated.
type Content struct {
	SMSEnabled   bool     `json:"sms_enabled"`
	EmailEnabled bool     `json:"email_enabled"`
	Settings     Settings `json:"settings"`
	SMSBody      string   `json:"sms_body"`
	EmailSubject string   `json:"email_subject"`
	EmailBody    string   `json:"email_body"`
}

// Audience is the selected audience as stored with the campaign: the rules
// (re-validated on the server) plus the counts at the time of the last check.
type Audience struct {
	Name            string                   `json:"name"`
	Match           string                   `json:"match"`
	Conditions      []segmentation.Condition `json:"conditions"`
	ConditionText   []string                 `json:"conditions_text"`
	Counts          segmentation.Counts      `json:"counts"`
	MaxFirstNameLen int                      `json:"max_first_name_len"`
	// Reachable members per content language (unsupported languages are
	// counted under FallbackLanguage).
	Languages map[string]int `json:"languages"`
}

func (a Audience) rules() segmentation.Alternative {
	return segmentation.Alternative{Name: a.Name, Match: a.Match, Conditions: a.Conditions}
}
