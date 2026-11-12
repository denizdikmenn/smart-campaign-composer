package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDecide(t *testing.T) {
	p := Policy{Base: 2 * time.Second, Max: 30 * time.Second, MaxRetryAfter: time.Minute, Jitter: 0.2, Rand: func() float64 { return 0.5 }}
	cases := []struct {
		name     string
		resp     Response
		err      error
		attempts int
		status   string
		delay    time.Duration
		reason   string
	}{
		{"accepted", Response{Status: 202}, nil, 1, StatusSent, 0, ""},
		{"429 with Retry-After", Response{Status: 429, RetryAfter: 7 * time.Second}, nil, 1, StatusRetrying, 7 * time.Second, "429"},
		{"429 Retry-After capped", Response{Status: 429, RetryAfter: time.Hour}, nil, 1, StatusRetrying, time.Minute, "429"},
		{"429 without Retry-After backs off", Response{Status: 429}, nil, 2, StatusRetrying, 4 * time.Second, "429"},
		{"5xx backs off exponentially", Response{Status: 503, Error: "service unavailable"}, nil, 3, StatusRetrying, 8 * time.Second, "503 service unavailable"},
		{"no response is retried", Response{}, errors.New("timeout"), 1, StatusRetrying, 2 * time.Second, "no response from provider: timeout"},
		{"4xx is permanent", Response{Status: 400, Error: "bad number"}, nil, 1, StatusFailed, 0, "400 bad number"},
		{"exhausted", Response{Status: 503}, nil, MaxAttempts, StatusFailed, 0, "gave up after 4 attempts: 503"},
		{"coded 4xx", Response{Status: 422, Code: "EMAIL_SUPPRESSED", Error: "suppressed"}, nil, 1, StatusFailed, 0, "422 EMAIL_SUPPRESSED: suppressed"},
		{"coded 5xx", Response{Status: 503, Code: "SMS_CARRIER_UNAVAILABLE", Error: "down"}, nil, 1, StatusRetrying, 2 * time.Second, "503 SMS_CARRIER_UNAVAILABLE: down"},
	}
	for _, c := range cases {
		o := p.Decide(c.resp, c.err, c.attempts)
		if o.Permanent != (c.name == "4xx is permanent" || c.name == "coded 4xx") {
			t.Errorf("%s: permanent %v", c.name, o.Permanent)
		}
		if o.Status != c.status || o.Delay != c.delay || !strings.HasPrefix(o.Error, c.reason) {
			t.Errorf("%s: got %+v", c.name, o)
		}
	}

	// Jitter stays within ±20% and the cap holds.
	for _, r := range []float64{0, 0.999} {
		p.Rand = func() float64 { return r }
		if d := p.backoff(1); d < 1600*time.Millisecond || d > 2400*time.Millisecond {
			t.Errorf("backoff(1) with r=%v: %v", r, d)
		}
		if d := p.backoff(10); d > 36*time.Second {
			t.Errorf("backoff(10) not capped: %v", d)
		}
	}
}

// fakeClock drives the limiter without real waiting.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time                           { return c.t }
func (c *fakeClock) sleep(_ context.Context, d time.Duration) { c.t = c.t.Add(d) }

func TestLimiterNeverExceedsRate(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	l := newLimiter(100, time.Second)
	l.now, l.sleep = clock.now, clock.sleep
	var calls []time.Time
	for range 350 {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, clock.now())
		clock.t = clock.t.Add(time.Millisecond) // each call takes a little time
	}
	if max := maxInWindow(calls, time.Second); max > 100 {
		t.Fatalf("%d calls in one second", max)
	}
	if elapsed := calls[len(calls)-1].Sub(calls[0]); elapsed < 3*time.Second {
		t.Fatalf("350 calls at 100/s finished in %v", elapsed)
	}
}

// maxInWindow is the largest number of times within any half-open window of length w.
func maxInWindow(ts []time.Time, w time.Duration) int {
	best, lo := 0, 0
	for hi := range ts {
		for ts[hi].Sub(ts[lo]) >= w {
			lo++
		}
		best = max(best, hi-lo+1)
	}
	return best
}
