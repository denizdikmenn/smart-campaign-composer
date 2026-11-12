package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smart-campaign-composer/backend/internal/db"
	"github.com/smart-campaign-composer/backend/internal/dbtest"
)

// setup creates n consenting customers, approved campaigns 1 and 2 and a
// draft campaign 3.
func setup(t *testing.T, n int) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		INSERT INTO customers (id, first_name, last_name, email, phone, city, email_consent, sms_consent)
		SELECT g, 'F', 'L', 'c' || g || '@example.com', '+90532' || lpad(g::text, 7, '0'), 'Istanbul', true, true
		FROM generate_series(1, %d) g;
		INSERT INTO campaigns (id, brief, audience, status, approved_version, approved_at, approved_snapshot) VALUES
		(1, 'a brief text', '{}', 'approved', 1, now(), '{}'),
		(2, 'a brief text', '{}', 'approved', 1, now(), '{}');
		INSERT INTO campaigns (id, brief, audience) VALUES (3, 'a brief text', '{}');`, n)); err != nil {
		t.Fatal(err)
	}
	return ctx, pool
}

// source yields the given channels for customers 1..n, alternating variants.
func source(n int, channels ...string) MessageSource {
	return func(_ context.Context, _ int64, fn func(Message) error) error {
		for id := int64(1); id <= int64(n); id++ {
			for _, ch := range channels {
				if err := fn(Message{CustomerID: id, Channel: ch, VariantPosition: int(id%2) + 1, Language: "tr", Source: "template",
					Body: fmt.Sprintf("hi %d", id)}); err != nil {
					return err
				}
			}
		}
		return nil
	}
}

func count(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDispatchCreatesJobsOnce(t *testing.T) {
	ctx, pool := setup(t, 20)
	src := source(20, "sms", "email")

	created, err := Dispatch(ctx, pool, 1, src)
	if err != nil || created != 40 {
		t.Fatalf("first dispatch: %d, %v", created, err)
	}
	if created, err := Dispatch(ctx, pool, 1, src); err != nil || created != 0 {
		t.Fatalf("repeated dispatch must create nothing: %d, %v", created, err)
	}

	// Concurrent Send clicks: exactly one batch.
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := Dispatch(ctx, pool, 2, src)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			total += n
			mu.Unlock()
		}()
	}
	wg.Wait()
	if total != 40 || count(t, pool, `SELECT count(*) FROM deliveries WHERE campaign_id = 2`) != 40 {
		t.Fatalf("concurrent dispatch created %d", total)
	}

	if _, err := Dispatch(ctx, pool, 3, src); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("draft campaign: %v", err)
	}
	if _, err := Dispatch(ctx, pool, 99, src); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing campaign: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE campaigns SET dispatched_at = NULL WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if created, _ := Dispatch(ctx, pool, 1, src); created != 0 {
		t.Fatalf("the unique constraint must still prevent duplicates: %d", created)
	}

	rep, err := CampaignReport(ctx, pool, 1)
	if err != nil || rep.Status != "queued" || rep.Totals.Queued != 40 || rep.Channels["sms"].Total != 20 ||
		len(rep.Variants) != 2 || rep.Variants[0].Total != 20 {
		t.Fatalf("report %+v, %v", rep, err)
	}
	if rep, _ := CampaignReport(ctx, pool, 3); rep.Status != "not_sent" {
		t.Fatalf("undispatched: %s", rep.Status)
	}
}

func TestConcurrentClaimsAreDisjoint(t *testing.T) {
	ctx, pool := setup(t, 200)
	if _, err := Dispatch(ctx, pool, 1, source(200, "sms")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				jobs, err := Claim(ctx, pool, "sms", 7, time.Minute)
				if err != nil {
					t.Error(err)
					return
				}
				if len(jobs) == 0 {
					return
				}
				mu.Lock()
				for _, j := range jobs {
					seen[j.ID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 200 {
		t.Fatalf("claimed %d of 200", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("job %d claimed %d times", id, n)
		}
	}
	if jobs, _ := Claim(ctx, pool, "email", 10, time.Minute); len(jobs) != 0 {
		t.Fatalf("claims are per channel: %d", len(jobs))
	}
}

func TestExpiredLeaseIsRecovered(t *testing.T) {
	ctx, pool := setup(t, 2)
	if _, err := Dispatch(ctx, pool, 1, source(2, "sms")); err != nil {
		t.Fatal(err)
	}
	// Worker A claims both, then "crashes" without recording anything.
	crashed, err := Claim(ctx, pool, "sms", 1, 50*time.Millisecond)
	if err != nil || len(crashed) != 1 || crashed[0].Attempts != 1 {
		t.Fatalf("claim: %+v, %v", crashed, err)
	}
	held, _ := Claim(ctx, pool, "sms", 1, time.Minute)
	if jobs, _ := Claim(ctx, pool, "sms", 10, time.Minute); len(jobs) != 0 {
		t.Fatalf("leased jobs must not be claimed again: %+v", jobs)
	}
	time.Sleep(100 * time.Millisecond)
	jobs, err := Claim(ctx, pool, "sms", 10, time.Minute)
	if err != nil || len(jobs) != 1 || jobs[0].ID != crashed[0].ID || jobs[0].Key() != crashed[0].Key() {
		t.Fatalf("recovered %+v (held %+v), %v", jobs, held, err)
	}
	if jobs[0].Attempts != 1 {
		t.Fatalf("a resumed attempt is not counted again: %d", jobs[0].Attempts)
	}
	// A late write from the crashed worker after the job finished elsewhere is ignored.
	if err := Complete(ctx, pool, jobs[0], Outcome{Status: StatusSent}, &Attempt{StartedAt: time.Now(), Status: 202, MessageID: "m1"}); err != nil {
		t.Fatal(err)
	}
	if err := Complete(ctx, pool, jobs[0], Outcome{Status: StatusFailed, Error: "late"}, nil); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM deliveries WHERE id = $1 AND status = 'sent'`, jobs[0].ID); n != 1 {
		t.Fatal("late outcome overwrote the recorded one")
	}
}

var testPolicy = Policy{Base: 20 * time.Millisecond, Max: 100 * time.Millisecond, MaxRetryAfter: time.Second}

// runWorker runs a worker until the campaign has no pending jobs.
func runWorker(t *testing.T, pool *pgxpool.Pool, campaignID int64, providers map[string]Provider, cfg Config) Report {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		NewWorker(pool, providers, cfg, slog.New(slog.DiscardHandler)).Run(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		rep, err := CampaignReport(context.Background(), pool, campaignID)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Totals.pending() == 0 {
			return rep
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("worker did not finish")
	return Report{}
}

func TestWorkerOutcomes(t *testing.T) {
	ctx, pool := setup(t, 7)
	if _, err := Dispatch(ctx, pool, 1, source(7, "sms")); err != nil {
		t.Fatal(err)
	}
	// Customer 7 withdraws SMS consent after the job was queued.
	if _, err := pool.Exec(ctx, `UPDATE customers SET sms_consent = false WHERE id = 7`); err != nil {
		t.Fatal(err)
	}
	key := func(c int) string { return fmt.Sprintf("campaign-1/customer-%d/sms", c) }
	script := map[string][]Fault{
		key(2): {FaultServer},                                        // transient, then accepted
		key(3): {FaultReject},                                        // permanent
		key(4): {FaultServer, FaultServer, FaultServer, FaultServer}, // exhausted
		key(5): {FaultRateLimit},                                     // 429 with Retry-After
		key(6): {FaultLostResponse},                                  // accepted, response lost
	}
	var mu sync.Mutex
	calls := map[string][]time.Time{}
	mock := &Mock{Pool: pool, Channel: "sms", RetryAfter: 300 * time.Millisecond,
		Faults: func(req Request, call int) Fault {
			if s := script[req.Key]; call <= len(s) {
				return s[call-1]
			}
			return FaultNone
		},
		OnCall: func(req Request, at time.Time) {
			mu.Lock()
			calls[req.Key] = append(calls[req.Key], at)
			mu.Unlock()
		}}
	cfg := Config{Name: "test", Rate: 100, Concurrency: 3, Lease: time.Minute, Poll: 10 * time.Millisecond, SendTimeout: time.Second, Policy: testPolicy}
	rep := runWorker(t, pool, 1, map[string]Provider{"sms": mock}, cfg)

	type row struct {
		status, lastError string
		attempts          int
		messageID         string
	}
	got := map[int]row{}
	rows, _ := pool.Query(ctx, `SELECT customer_id, status, COALESCE(last_error, ''), attempts, COALESCE(provider_message_id, '') FROM deliveries WHERE campaign_id = 1`)
	for rows.Next() {
		var c int
		var r row
		if err := rows.Scan(&c, &r.status, &r.lastError, &r.attempts, &r.messageID); err != nil {
			t.Fatal(err)
		}
		got[c] = r
	}
	want := map[int]struct {
		status   string
		attempts int
		err      string
	}{
		1: {StatusSent, 1, ""},
		2: {StatusSent, 2, "503 SMS_PROVIDER_UNAVAILABLE: service unavailable"},
		3: {StatusFailed, 1, "400 SMS_REJECTED: recipient rejected"},
		4: {StatusFailed, 4, "gave up after 4 attempts: 503"},
		5: {StatusSent, 2, "429"},
		6: {StatusSent, 2, "no response from provider: timeout"},
		7: {StatusSkipped, 1, "sms consent withdrawn"}, // the claim counts; no provider call
	}
	for c, w := range want {
		r := got[c]
		if r.status != w.status || r.attempts != w.attempts || !strings.HasPrefix(r.lastError, w.err) {
			t.Errorf("customer %d: %+v, want %+v", c, r, w)
		}
		if r.status == StatusSent && r.messageID == "" {
			t.Errorf("customer %d: no provider message id", c)
		}
	}
	if len(calls[key(7)]) != 0 {
		t.Error("a customer without consent must not reach the provider")
	}
	if c := calls[key(5)]; len(c) != 2 || c[1].Sub(c[0]) < 300*time.Millisecond {
		t.Errorf("429 retry did not wait for Retry-After: %v", c)
	}
	// The lost response was retried with the same key and accepted only once.
	if n := count(t, pool, `SELECT count(*) FROM mock_provider_messages WHERE idempotency_key = $1`, key(6)); n != 1 || len(calls[key(6)]) != 2 {
		t.Errorf("lost response: %d accepted, %d calls", n, len(calls[key(6)]))
	}
	var accepted string
	_ = pool.QueryRow(ctx, `SELECT message_id FROM mock_provider_messages WHERE idempotency_key = $1`, key(6)).Scan(&accepted)
	if accepted != got[6].messageID {
		t.Errorf("replay returned %q, first acceptance was %q", got[6].messageID, accepted)
	}
	if n := count(t, pool, `SELECT count(*) FROM mock_provider_messages`); n != 4 {
		t.Errorf("accepted messages %d, want 4", n)
	}

	// Every provider call is in the attempt history, with its code; skips made no call.
	type att struct {
		n       int
		status  *int
		code    *string
		outcome string
	}
	history := func(c int) []att {
		rows, err := pool.Query(ctx, `SELECT a.attempt, a.http_status, a.code, a.outcome FROM delivery_attempts a
			JOIN deliveries d ON d.id = a.delivery_id WHERE d.customer_id = $1 AND a.latency_ms >= 0 ORDER BY a.id`, c)
		if err != nil {
			t.Fatal(err)
		}
		var out []att
		for rows.Next() {
			var a att
			if err := rows.Scan(&a.n, &a.status, &a.code, &a.outcome); err != nil {
				t.Fatal(err)
			}
			out = append(out, a)
		}
		return out
	}
	for c, w := range want {
		if h := history(c); len(h) != w.attempts && w.status != StatusSkipped || w.status == StatusSkipped && len(h) != 0 {
			t.Errorf("customer %d: %d attempts recorded, want %d", c, len(h), w.attempts)
		}
	}
	if h := history(4); h[0].n != 1 || h[3].n != 4 || *h[3].code != "SMS_PROVIDER_UNAVAILABLE" || *h[3].status != 503 || h[2].outcome != StatusRetrying || h[3].outcome != StatusFailed {
		t.Errorf("exhausted history %+v", h)
	}
	if h := history(6); h[0].status != nil || *h[0].code != "NO_RESPONSE" || *h[1].status != 202 || *h[1].code != "ACCEPTED" || h[1].outcome != StatusSent {
		t.Errorf("lost response history %+v", h)
	}
	if n := count(t, pool, `SELECT count(*) FROM deliveries WHERE permanent`); n != 1 {
		t.Errorf("only the 400 is permanent, got %d", n)
	}

	// Monitoring counters.
	tot := rep.Totals
	if rep.Status != "completed_with_failures" || tot.Total != 7 || tot.Sent != 4 || tot.Failed != 2 || tot.Skipped != 1 || tot.Retries != 6 || rep.FinishedAt == nil {
		t.Fatalf("report %+v", rep)
	}
	if len(rep.Errors) != 3 || rep.WorkerSeenAt == nil {
		t.Fatalf("errors %+v, worker %v", rep.Errors, rep.WorkerSeenAt)
	}
	if v := rep.Variants; len(v) != 2 || v[0].Sent+v[1].Sent != 4 || v[0].Total+v[1].Total != 7 {
		t.Fatalf("variants %+v", v)
	}
}

func TestRateLimitPerChannel(t *testing.T) {
	ctx, pool := setup(t, 150)
	if _, err := Dispatch(ctx, pool, 1, source(150, "sms", "email")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := map[string][]time.Time{}
	providers := map[string]Provider{}
	for _, ch := range []string{"sms", "email"} {
		providers[ch] = &Mock{Pool: pool, Channel: ch, OnCall: func(_ Request, at time.Time) {
			mu.Lock()
			calls[ch] = append(calls[ch], at)
			mu.Unlock()
		}}
	}
	cfg := Config{Name: "test", Rate: 100, Concurrency: 20, Lease: time.Minute, Poll: 10 * time.Millisecond, SendTimeout: time.Second, Policy: testPolicy}
	start := time.Now()
	rep := runWorker(t, pool, 1, providers, cfg)
	elapsed := time.Since(start)
	if rep.Totals.Sent != 300 {
		t.Fatalf("sent %d", rep.Totals.Sent)
	}
	for ch, ts := range calls {
		sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
		// 10ms of slack for the gap between the limiter and the provider call.
		if n := maxInWindow(ts, 990*time.Millisecond); n > 100 {
			t.Errorf("%s: %d calls within one second", ch, n)
		}
		if span := ts[len(ts)-1].Sub(ts[0]); span < 990*time.Millisecond {
			t.Errorf("%s: 150 calls in %v exceeds 100/s", ch, span)
		}
	}
	// Independent limits: both channels progress at the same time.
	if elapsed > 1900*time.Millisecond {
		t.Errorf("channels look serialized: %v", elapsed)
	}
}
