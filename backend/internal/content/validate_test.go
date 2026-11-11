package content

import (
	"strings"
	"testing"
)

func codes(issues []Issue, field string) []string {
	var out []string
	for _, i := range issues {
		if field == "" || i.Field == field {
			out = append(out, i.Code)
		}
	}
	return out
}

func valid() Content {
	return Content{SMSEnabled: true, EmailEnabled: true,
		Settings: Settings{Tone: "friendly", Occasion: "promotion", Length: "short", CTA: "shop_now"},
		SMSBody:  "Hi {{first_name}}, new arrivals are in. Shop now.", EmailSubject: "{{first_name}}, new arrivals", EmailBody: "Hi {{first_name}},\n\nShop now.\n\nThe team"}
}

func TestValidate(t *testing.T) {
	brief := "Send a 30% discount reminder to Istanbul cart abandoners."

	if got := Validate(valid(), brief, 10); len(got) != 0 {
		t.Fatalf("valid content: %+v", got)
	}

	t.Run("sms length is checked after substitution", func(t *testing.T) {
		c := valid()
		// 150 characters as written, including one {{first_name}} (14 chars).
		c.SMSBody = "{{first_name}} " + strings.Repeat("a", 135)
		if got := codes(Validate(c, brief, 10), "sms_body"); len(got) != 0 {
			t.Fatalf("10-letter names fit (146 chars): %v", got)
		}
		got := Validate(c, brief, 30) // 166 chars personalized
		if !Blocking(got) || codes(got, "sms_body")[0] != "too_long_personalized" {
			t.Fatalf("30-letter names overflow: %+v", got)
		}
		// Raw template over 160 but personalized fits: allowed.
		c.SMSBody = "{{first_name}} " + strings.Repeat("a", 148) // 163 raw, 155 for 6-letter names
		if got := Validate(c, brief, 6); Blocking(got) {
			t.Fatalf("personalized fits: %+v", got)
		}
		c.SMSBody = strings.Repeat("a", 161)
		if got := codes(Validate(c, brief, 6), "sms_body"); len(got) != 1 || got[0] != "too_long" {
			t.Fatalf("plain 161: %v", got)
		}
	})

	t.Run("subject limit", func(t *testing.T) {
		c := valid()
		c.EmailSubject = strings.Repeat("s", 80)
		if Blocking(Validate(c, brief, 10)) {
			t.Fatal("80 is allowed")
		}
		c.EmailSubject = strings.Repeat("s", 81)
		if !Blocking(Validate(c, brief, 10)) {
			t.Fatal("81 is not")
		}
	})

	t.Run("required only for enabled channels", func(t *testing.T) {
		c := valid()
		c.SMSBody = "  "
		if got := codes(Validate(c, brief, 10), ""); len(got) != 1 || got[0] != "required" {
			t.Fatalf("empty sms: %v", got)
		}
		c.SMSEnabled = false
		if got := Validate(c, brief, 10); len(got) != 0 {
			t.Fatalf("disabled channel is not validated: %+v", got)
		}
		c.EmailEnabled = false
		if got := codes(Validate(c, brief, 10), ""); len(got) != 1 || got[0] != "no_channel" {
			t.Fatalf("no channel: %v", got)
		}
	})

	t.Run("placeholders", func(t *testing.T) {
		for body, code := range map[string]string{
			"Hi {{last_name}}":     "unsupported_variable",
			"Hi {{ first_name }}":  "unsupported_variable",
			"Hi {{first_name}":     "malformed_variable",
			"Use {{coupon}} today": "unsupported_variable",
		} {
			c := valid()
			c.SMSBody = body
			if got := codes(Validate(c, brief, 10), "sms_body"); len(got) == 0 || got[0] != code {
				t.Errorf("%q: %v, want %s", body, got, code)
			}
		}
	})

	t.Run("invented facts are warnings", func(t *testing.T) {
		c := valid()
		c.SMSBody = "Hi {{first_name}}, %30 off your cart!"           // same discount, Turkish style: fine
		c.EmailBody = "Get 40% off with code SAVE40 and 100 TL gift." // invented
		got := Validate(c, brief, 10)
		if Blocking(got) {
			t.Fatalf("facts must not block: %+v", got)
		}
		if n := len(codes(got, "email_body")); n != 3 || len(codes(got, "sms_body")) != 0 {
			t.Fatalf("want 3 email warnings, got %+v", got)
		}
	})
}

func TestRender(t *testing.T) {
	if got := Render("Hi {{first_name}}!", "Ayşe"); got != "Hi Ayşe!" {
		t.Fatal(got)
	}
	if PersonalizedLen("Hi {{first_name}}!", 4) != 8 {
		t.Fatal("personalized length")
	}
}
