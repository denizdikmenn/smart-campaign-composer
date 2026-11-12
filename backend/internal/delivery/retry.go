package delivery

import (
	"fmt"
	"math/rand/v2"
	"time"
)

// MaxAttempts is the total number of provider attempts per message
// (the initial attempt plus up to 3 retries).
const MaxAttempts = 4

// Delivery statuses. queued, sending and retrying are pending; the rest are terminal.
const (
	StatusQueued   = "queued"
	StatusSending  = "sending"
	StatusRetrying = "retrying"
	StatusSent     = "sent"
	StatusFailed   = "failed"
	StatusSkipped  = "skipped"
)

// Policy is the retry schedule. Backoff for attempt n (1-based) is
// Base·2^(n-1), capped at Max, with ±Jitter randomization.
type Policy struct {
	Base          time.Duration
	Max           time.Duration
	MaxRetryAfter time.Duration // cap for a provider's Retry-After
	Jitter        float64       // fraction, e.g. 0.2 = ±20%
	Rand          func() float64
}

// DefaultPolicy retries after ~2s, ~4s and ~8s.
var DefaultPolicy = Policy{Base: 2 * time.Second, Max: 30 * time.Second, MaxRetryAfter: time.Minute, Jitter: 0.2}

// Outcome is what to record after one provider attempt.
type Outcome struct {
	Status    string        // sent | retrying | failed
	Delay     time.Duration // until the next attempt (retrying only)
	Code      string        // provider code, or NO_RESPONSE
	Error     string        // marketer-facing reason; stable strings so they group well
	Permanent bool          // failed because the provider rejected the message (never retryable)
}

// Decide classifies one attempt. attempts counts the attempts of the current
// round (a manual retry starts a new round). err is a transport-level
// failure (no response, timeout): the provider may or may not have accepted
// the message, so it is retried with the same idempotency key, like a 5xx.
func (p Policy) Decide(resp Response, err error, attempts int) Outcome {
	var reason, code string
	var delay time.Duration
	switch {
	case err != nil:
		code = "NO_RESPONSE"
		reason, delay = "no response from provider: "+err.Error(), p.backoff(attempts)
	case resp.Status >= 200 && resp.Status < 300:
		return Outcome{Status: StatusSent, Code: resp.Code}
	case resp.Status == 429:
		code = orDefault(resp.Code, "RATE_LIMITED")
		reason, delay = "429 rate limited by provider", p.backoff(attempts)
		if resp.RetryAfter > 0 {
			delay = min(resp.RetryAfter, p.MaxRetryAfter)
		}
	case resp.Status >= 500:
		code = resp.Code
		reason, delay = describe(resp, "provider error"), p.backoff(attempts)
	default: // other 4xx: the request itself is wrong, retrying cannot help
		return Outcome{Status: StatusFailed, Code: resp.Code, Error: describe(resp, "rejected by provider"), Permanent: true}
	}
	if attempts >= MaxAttempts {
		return Outcome{Status: StatusFailed, Code: code, Error: fmt.Sprintf("gave up after %d attempts: %s", attempts, reason)}
	}
	return Outcome{Status: StatusRetrying, Delay: delay, Code: code, Error: reason}
}

// describe is "503 SMS_CARRIER_UNAVAILABLE: carrier temporarily unavailable".
func describe(r Response, def string) string {
	msg := orDefault(r.Error, def)
	if r.Code != "" {
		return fmt.Sprintf("%d %s: %s", r.Status, r.Code, msg)
	}
	return fmt.Sprintf("%d %s", r.Status, msg)
}

func (p Policy) backoff(attempt int) time.Duration {
	d := p.Base << max(attempt-1, 0)
	if d > p.Max || d <= 0 {
		d = p.Max
	}
	r := rand.Float64
	if p.Rand != nil {
		r = p.Rand
	}
	return time.Duration(float64(d) * (1 + p.Jitter*(2*r()-1)))
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
