package content

import (
	"strings"
	"testing"

	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

// Regressions from the hardening eval (evals/hardening/reports/latest.md).

func TestEligibilityMismatch(t *testing.T) { // G08
	repeat := Audience{Match: "all", Conditions: []segmentation.Condition{{Field: "order_count", Op: "gte", Numbers: []float64{4}}}}
	never := Audience{Match: "all", Conditions: []segmentation.Condition{{Field: "order_count", Op: "eq", Numbers: []float64{0}}}}
	either := Audience{Match: "any", Conditions: []segmentation.Condition{{Field: "order_count", Op: "gte", Numbers: []float64{4}}, {Field: "city", Op: "in", Strings: []string{"Izmir"}}}}
	d := Draft{Content: Content{SMSBody: "Get 100 TL off your first order with HELLO100", EmailSubject: "Welcome", EmailBody: "Hi"}}
	if is := eligibilityIssues(d, repeat); len(is) != 1 || !is[0].Blocking {
		t.Fatalf("repeat buyers + first order: %v", is)
	}
	if is := eligibilityIssues(d, never); len(is) != 0 {
		t.Fatalf("never ordered audience flagged: %v", is)
	}
	if is := eligibilityIssues(d, either); len(is) != 0 {
		t.Fatalf("'any' audience flagged: %v", is)
	}
	tr := Draft{Content: Content{SMSBody: "x", EmailSubject: "x", EmailBody: "x"}, Translations: map[string]Version{"tr": {SMSBody: "İlk siparişinde 100 TL indirim"}}}
	if is := eligibilityIssues(tr, repeat); len(is) != 1 {
		t.Fatalf("translated first-order wording not flagged: %v", is)
	}
}

func TestFeedbackFactsNotFlagged(t *testing.T) { // G03, G13
	c := Campaign{Brief: "Remind cart abandoners to come back.", feedback: "Add code WINTER25 for 25% off."}
	content := Content{SMSEnabled: true, SMSBody: "Use WINTER25 for 25% off today."}
	for _, i := range Validate(content, c.facts(), 10) {
		if i.Code == "unverified_fact" {
			t.Fatalf("feedback fact flagged: %s", i.Message)
		}
	}
	for _, i := range Validate(Content{SMSEnabled: true, SMSBody: "Use SPRING10 for 10% off."}, c.facts(), 10) {
		if i.Code == "unverified_fact" {
			return
		}
	}
	t.Fatal("invented code not flagged")
}

func TestReplyNotes(t *testing.T) { // G07, G12
	r, err := parseReply(`{"settings":{"tone":"friendly","occasion":"promotion","length":"short","cta":"shop_now"},"sms_body":"a","email_subject":"b","email_body":"c","notes":"  Images are not supported. "}`)
	if err != nil || r.Notes != "Images are not supported." {
		t.Fatalf("notes not parsed: %q %v", r.Notes, err)
	}
	leaked := `{"settings":{"tone":"friendly","occasion":"promotion","length":"short","cta":"shop_now"},"sms_body":"Find your next favorite.','notes':","email_subject":"b","email_body":"c"}`
	if _, err := parseReply(leaked); err == nil {
		t.Fatal("leaked JSON key accepted")
	}
	if _, err := parseReply(`{"settings":{"tone":"friendly","occasion":"promotion","length":"short","cta":"shop_now"},"sms_body":"Notes: bring a friend!","email_subject":"b","email_body":"Our 'settings' page: see notes."}`); err != nil {
		t.Fatalf("ordinary text rejected: %v", err)
	}
	if !strings.Contains(systemPrompt, "notes (optional):") {
		t.Fatal("prompt does not explain notes")
	}
}
