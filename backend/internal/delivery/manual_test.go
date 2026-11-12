package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestManualRetry(t *testing.T) {
	ctx, pool := setup(t, 5)
	if _, err := Dispatch(ctx, pool, 1, source(5, "sms")); err != nil {
		t.Fatal(err)
	}
	key := func(c int) string { return fmt.Sprintf("campaign-1/customer-%d/sms", c) }
	four := func(f Fault) []Fault { return []Fault{f, f, f, f} }
	script := map[string][]Fault{
		key(1): four(FaultServer),       // exhausts, then recovers on a manual retry
		key(2): {FaultReject},           // permanent
		key(4): four(FaultLostResponse), // accepted on call 1, but no response ever arrived
		key(5): four(FaultServer),       // exhausts; consent is withdrawn before the manual retry
	}
	mock := &Mock{Pool: pool, Channel: "sms", Faults: func(req Request, call int) Fault {
		if s := script[req.Key]; call <= len(s) {
			return s[call-1]
		}
		return FaultNone
	}}
	cfg := Config{Name: "test", Rate: 100, Concurrency: 3, Lease: time.Minute, Poll: 10 * time.Millisecond, SendTimeout: time.Second, Policy: testPolicy}
	providers := map[string]Provider{"sms": mock}
	rep := runWorker(t, pool, 1, providers, cfg)
	if rep.Totals.Failed != 4 || rep.Totals.Sent != 1 || rep.RetryableFailed != 3 {
		t.Fatalf("after first run %+v, retryable %d", rep.Totals, rep.RetryableFailed)
	}
	id := func(c int) int64 {
		return int64(count(t, pool, `SELECT id FROM deliveries WHERE customer_id = $1`, c))
	}

	// Single retries that must be refused, with the reason.
	var nr *NotRetryableError
	if _, err := RetryFailed(ctx, pool, 1, id(2)); !errors.As(err, &nr) || !strings.Contains(nr.Reason, "permanently rejected") {
		t.Fatalf("permanent: %v", err)
	}
	if _, err := RetryFailed(ctx, pool, 1, id(3)); !errors.As(err, &nr) || !strings.Contains(nr.Reason, "already accepted") {
		t.Fatalf("sent: %v", err)
	}
	if _, err := RetryFailed(ctx, pool, 2, id(1)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other campaign: %v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE customers SET sms_consent = false WHERE id = 5`); err != nil {
		t.Fatal(err)
	}
	res, err := RetryFailed(ctx, pool, 1, 0)
	if err != nil || res.Requeued != 3 || res.Permanent != 1 {
		t.Fatalf("retry failed: %+v, %v", res, err)
	}
	// Clicking again while they are pending changes nothing.
	if res, _ := RetryFailed(ctx, pool, 1, 0); res.Requeued != 0 {
		t.Fatalf("second click requeued %d", res.Requeued)
	}
	if n := count(t, pool, `SELECT count(*) FROM deliveries WHERE campaign_id = 1`); n != 5 {
		t.Fatalf("a retry must reuse the delivery rows: %d", n)
	}

	rep = runWorker(t, pool, 1, providers, cfg)
	type row struct {
		status    string
		attempts  int
		messageID string
	}
	get := func(c int) row {
		var r row
		if err := pool.QueryRow(ctx, `SELECT status, attempts, COALESCE(provider_message_id, '') FROM deliveries WHERE customer_id = $1`, c).
			Scan(&r.status, &r.attempts, &r.messageID); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := get(1); r.status != StatusSent || r.attempts != 5 {
		t.Errorf("customer 1 after manual retry: %+v", r)
	}
	if n := count(t, pool, `SELECT count(*) FROM delivery_attempts a JOIN deliveries d ON d.id = a.delivery_id
		WHERE d.customer_id = 1 AND a.manual AND a.attempt = 5`); n != 1 {
		t.Error("the first attempt after a manual retry is marked manual")
	}
	// Customer 4 had been accepted already: the same key returns the original message, no second send.
	var original string
	_ = pool.QueryRow(ctx, `SELECT message_id FROM mock_provider_messages WHERE idempotency_key = $1`, key(4)).Scan(&original)
	if r := get(4); r.status != StatusSent || r.messageID != original ||
		count(t, pool, `SELECT count(*) FROM mock_provider_messages WHERE idempotency_key = $1`, key(4)) != 1 {
		t.Errorf("customer 4: %+v, original %q", r, original)
	}
	// Consent is not bypassed by a manual retry.
	if r := get(5); r.status != StatusSkipped {
		t.Errorf("customer 5 withdrew consent: %+v", r)
	}
	if r := get(2); r.status != StatusFailed || r.attempts != 1 {
		t.Errorf("permanent failure must stay untouched: %+v", r)
	}
	if rep.Totals.Sent != 3 || rep.Totals.Failed != 1 || rep.Totals.Skipped != 1 || rep.RetryableFailed != 0 {
		t.Errorf("final report %+v", rep.Totals)
	}
}

func TestDispatchRecordsSkips(t *testing.T) {
	ctx, pool := setup(t, 3)
	src := func(_ context.Context, _ int64, fn func(Message) error) error {
		for id := int64(1); id <= 3; id++ {
			m := Message{CustomerID: id, Channel: "sms", Language: "tr", Source: "template", Body: "hi"}
			if id == 3 {
				m.Body, m.Skip = "", "not eligible: no SMS consent"
			}
			if err := fn(m); err != nil {
				return err
			}
		}
		return nil
	}
	if n, err := Dispatch(ctx, pool, 1, src); err != nil || n != 3 {
		t.Fatalf("dispatch %d, %v", n, err)
	}
	rep, _ := CampaignReport(ctx, pool, 1)
	if rep.Status != "queued" || rep.Totals.Skipped != 1 || rep.Totals.Queued != 2 || len(rep.Errors) != 1 || rep.Errors[0].Status != StatusSkipped {
		t.Fatalf("report %+v", rep)
	}
	if jobs, _ := Claim(ctx, pool, "sms", 10, time.Minute); len(jobs) != 2 {
		t.Fatalf("skipped deliveries are never claimed: %d", len(jobs))
	}
	onlySkips := func(_ context.Context, _ int64, fn func(Message) error) error {
		return fn(Message{CustomerID: 1, Channel: "sms", Language: "tr", Source: "template", Skip: "not eligible: no SMS consent"})
	}
	if _, err := Dispatch(ctx, pool, 2, onlySkips); !errors.Is(err, ErrNoRecipients) {
		t.Fatalf("nobody reachable: %v", err)
	}
	// Recipient-level listing: filter, paging, masked address.
	p, err := Messages(ctx, pool, 1, MessageFilter{Status: StatusSkipped, PageSize: 10})
	if err != nil || p.Total != 1 || p.Items[0].CustomerID != 3 || p.Items[0].To != "•••0003" || p.Items[0].Retryable {
		t.Fatalf("messages %+v, %v", p, err)
	}
	if p, _ := Messages(ctx, pool, 1, MessageFilter{PageSize: 2, Page: 2}); p.Total != 3 || len(p.Items) != 1 {
		t.Fatalf("page 2 %+v", p)
	}
}
