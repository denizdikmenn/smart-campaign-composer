package delivery

import (
	"fmt"
	"testing"
)

// address returns the first test address in the given bucket.
func address(channel string, bucket int) string {
	for i := 0; ; i++ {
		a := fmt.Sprintf("c%d@example.com", i)
		if channel == "sms" {
			a = fmt.Sprintf("+90532%07d", i)
		}
		if Bucket(a) == bucket {
			return a
		}
	}
}

func TestSyntheticRecipients(t *testing.T) {
	p := Policy{Base: 1, Max: 1}
	cases := []struct {
		channel string
		bucket  int
		calls   []string // outcome per call; "" = no rule (normal sending)
	}{
		{"sms", 0, []string{"400 SMS_INVALID_NUMBER permanent", "400 SMS_INVALID_NUMBER permanent"}},
		{"sms", 1, []string{"403 SMS_BLOCKED_BY_CARRIER permanent"}},
		{"sms", 2, []string{"503 SMS_CARRIER_UNAVAILABLE retrying", "503 SMS_CARRIER_UNAVAILABLE retrying", ""}},
		{"email", 0, []string{"400 EMAIL_INVALID_ADDRESS permanent"}},
		{"email", 1, []string{"422 EMAIL_SUPPRESSED permanent"}},
		{"email", 2, []string{"503 EMAIL_MAILBOX_BUSY retrying", ""}},
		{"email", 3, []string{"503 EMAIL_MAILBOX_BUSY retrying", "503 EMAIL_MAILBOX_BUSY retrying", "503 EMAIL_MAILBOX_BUSY retrying"}},
		{"sms", 50, []string{""}},
		{"email", 50, []string{""}},
	}
	for _, c := range cases {
		to := address(c.channel, c.bucket)
		for i, want := range c.calls {
			resp, ok := SyntheticRecipients(Request{Channel: c.channel, To: to}, i+1)
			got := ""
			if ok {
				o := p.Decide(resp, nil, 1)
				kind := "retrying"
				if o.Permanent {
					kind = "permanent"
				}
				got = fmt.Sprintf("%d %s %s", resp.Status, resp.Code, kind)
			}
			if got != want {
				t.Errorf("%s bucket %d call %d: %q, want %q", c.channel, c.bucket, i+1, got, want)
			}
		}
	}
	// Reproducible: the bucket depends only on the address.
	if Bucket("Ada@Example.com") != Bucket("ada@example.com") {
		t.Error("addresses are case-insensitive")
	}
}

func TestFaultRates(t *testing.T) {
	r, err := ParseFaultRates("5xx=0.2, lost=0", DefaultFaultRates)
	if err != nil || r.Server != 0.2 || r.LostResponse != 0 || r.RateLimit != DefaultFaultRates.RateLimit {
		t.Fatalf("%+v, %v", r, err)
	}
	for _, bad := range []string{"5xx", "5xx=2", "foo=0.1", "429=0.5,5xx=0.6"} {
		if _, err := ParseFaultRates(bad, DefaultFaultRates); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	// Same seed, same faults; the observed rate matches the configuration.
	a, b := RandomFaults(r, 7), RandomFaults(r, 7)
	server := 0
	for i := range 5000 {
		req := Request{Key: fmt.Sprintf("k%d", i)}
		if a(req, 1) != b(req, 1) {
			t.Fatal("faults are not reproducible")
		}
		if a(req, 1) == FaultServer {
			server++
		}
	}
	if server < 900 || server > 1100 {
		t.Errorf("5xx rate %d/5000, want ~20%%", server)
	}
}
