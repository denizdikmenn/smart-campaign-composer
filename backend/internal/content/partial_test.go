package content

import (
	"encoding/json"
	"testing"
)

func TestPartialStrings(t *testing.T) {
	full := `{"settings":{"tone":"friendly","occasion":"cart_recovery","length":"short","cta":"complete_purchase"},` +
		`"sms_body":"Hi {{first_name}}, \"quoted\"\nline ✓ 😀 %30","email_subject":"S","email_body":"B"}`

	cases := []struct {
		buf  string
		want map[string]string
	}{
		{`{"settings":{"to`, map[string]string{}},
		{`{"settings":{"tone":"frie`, map[string]string{"tone": "frie"}},
		{`{"settings":{"tone":"friendly","occasion":`, map[string]string{"tone": "friendly"}},
		{`{"sms_body":"Hi \`, map[string]string{"sms_body": "Hi "}},           // escape split across chunks
		{`{"sms_body":"Hi \u00`, map[string]string{"sms_body": "Hi "}},        // \u split
		{`{"sms_body":"a \ud83d`, map[string]string{"sms_body": "a "}},        // surrogate pair split
		{`{"sms_body":"a \ud83d\ude00`, map[string]string{"sms_body": "a 😀"}}, // pair complete, string not
		{"{\"sms_body\":\"x \xe2\x9c", map[string]string{"sms_body": "x "}},   // UTF-8 rune split
		{`{"sms_body":"a:b, \"c\": d`, map[string]string{"sms_body": `a:b, "c": d`}},
	}
	for _, c := range cases {
		got := partialStrings(c.buf)
		if len(got) != len(c.want) {
			t.Errorf("%q: got %v, want %v", c.buf, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%q: %s = %q, want %q", c.buf, k, got[k], v)
			}
		}
	}

	// Every prefix decodes without panicking, and the complete reply matches encoding/json.
	for i := range len(full) {
		partialStrings(full[:i])
	}
	var r Reply
	if err := json.Unmarshal([]byte(full), &r); err != nil {
		t.Fatal(err)
	}
	got := partialStrings(full)
	if got["sms_body"] != r.SMSBody || got["cta"] != "complete_purchase" || got["email_body"] != "B" {
		t.Fatalf("complete parse mismatch: %v", got)
	}
}
