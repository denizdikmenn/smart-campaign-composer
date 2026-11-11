package content

import (
	"math"
	"testing"

	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

func TestVariantAssignment(t *testing.T) {
	for _, n := range []int{2, 3} {
		counts := make([]int, n+1)
		for cid := int64(1); cid <= 3000; cid++ {
			v := variantFor(42, cid, n)
			if v < 1 || v > n || v != variantFor(42, cid, n) {
				t.Fatalf("n=%d customer %d: variant %d unstable or out of range", n, cid, v)
			}
			counts[v]++
		}
		for v := 1; v <= n; v++ {
			if share := float64(counts[v]) / 3000; math.Abs(share-1/float64(n)) > 0.04 {
				t.Errorf("n=%d variant %d share %.3f", n, v, share)
			}
		}
	}
	// Different campaigns shuffle customers independently.
	same := 0
	for cid := int64(1); cid <= 1000; cid++ {
		if variantFor(1, cid, 2) == variantFor(2, cid, 2) {
			same++
		}
	}
	if same < 400 || same > 600 {
		t.Errorf("assignments look correlated across campaigns: %d/1000", same)
	}
}

func TestResolve(t *testing.T) {
	trDraft := Draft{Version: 1, Language: "tr", Content: Content{SMSEnabled: true, EmailEnabled: true,
		SMSBody: "Merhaba {{first_name}}", EmailSubject: "Konu", EmailBody: "Gövde"},
		Translations: map[string]Version{"en": {SMSBody: "Hi {{first_name}}", EmailSubject: "Subject", EmailBody: "Body"}}}
	other := trDraft
	other.Version, other.SMSBody = 2, "Selam {{first_name}}"
	member := func(id int64, lang string, sms, email bool) segmentation.Member {
		return segmentation.Member{ID: id, FirstName: "Ada", Language: lang, SMSConsent: sms, EmailConsent: email}
	}

	r := &resolver{campaignID: 7, mode: ModeStandard, base: trDraft}
	for lang, want := range map[string]string{"tr": "Merhaba Ada", "en": "Hi Ada", "de": "Hi Ada", "xx": "Hi Ada"} {
		msgs := r.resolve(member(1, lang, true, false))
		if len(msgs) != 1 || msgs[0].Body != want || msgs[0].Source != "template" {
			t.Errorf("%s: %+v", lang, msgs)
		}
	}
	// Without an English version, unsupported languages get the base language.
	noEN := trDraft
	noEN.Translations = map[string]Version{}
	if m := (&resolver{mode: ModeStandard, base: noEN}).resolve(member(1, "de", true, false)); m[0].Language != "tr" {
		t.Errorf("fallback to base: %+v", m)
	}
	// Consent decides channels; nobody without consent gets anything.
	if m := r.resolve(member(1, "tr", false, false)); len(m) != 0 {
		t.Errorf("no consent: %+v", m)
	}

	// A/B: both channels of a customer use the same variant.
	ab := &resolver{campaignID: 7, mode: ModeStandard, variants: []Draft{trDraft, other}}
	for cid := int64(1); cid <= 200; cid++ {
		msgs := ab.resolve(member(cid, "tr", true, true))
		if len(msgs) != 2 || msgs[0].VariantPosition != msgs[1].VariantPosition || msgs[0].VariantPosition != variantFor(7, cid, 2) {
			t.Fatalf("customer %d: %+v", cid, msgs)
		}
	}

	// Individual: generated content wins; anything else falls back to the base.
	ind := &resolver{mode: ModeIndividual, base: trDraft, personal: map[int64]PersonalMessage{
		1: {Status: "generated", Language: "en", Version: Version{SMSBody: "Hi Ada, your fashion picks", EmailSubject: "s", EmailBody: "b"}},
		2: {Status: "failed"},
	}}
	if m := ind.resolve(member(1, "en", true, false)); m[0].Source != "individual" || m[0].Body != "Hi Ada, your fashion picks" {
		t.Errorf("individual: %+v", m)
	}
	if m := ind.resolve(member(2, "tr", true, false)); m[0].Source != "fallback" || m[0].Body != "Merhaba Ada" {
		t.Errorf("failed individual: %+v", m)
	}

	// Groups: the member's group content in their language; group id attached.
	g := &resolver{mode: ModeGroups, base: trDraft, groups: map[int]Group{2: {Position: 2, Content: map[string]Version{"tr": {SMSBody: "Grup {{first_name}}"}}}}}
	mm := member(1, "tr", true, false)
	mm.Group = 2
	if m := g.resolve(mm); m[0].Source != "group" || m[0].Body != "Grup Ada" || *m[0].GroupPosition != 2 {
		t.Errorf("group: %+v", m)
	}
}
