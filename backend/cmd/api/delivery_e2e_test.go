package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/smart-campaign-composer/backend/internal/content"
	"github.com/smart-campaign-composer/backend/internal/db"
	"github.com/smart-campaign-composer/backend/internal/dbtest"
	"github.com/smart-campaign-composer/backend/internal/delivery"
	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

// Approve → Send → queue → mock provider → retry/complete → monitoring,
// through the real HTTP handlers, PostgreSQL and the worker (no Claude).
func TestDeliveryEndToEnd(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// 10 customers in Istanbul: all take email, even ids take SMS.
	if _, err := pool.Exec(ctx, `INSERT INTO customers (id, first_name, last_name, email, phone, city, email_consent, sms_consent)
		SELECT g, 'Ada', 'L', 'c' || g || '@example.com', '+90532000000' || (g - 1), 'Istanbul', true, g % 2 = 0
		FROM generate_series(1, 10) g`); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	seg := segmentation.NewService(pool, segmentation.Mock{}, log)
	camp := content.NewService(pool, content.Mock{}, seg, log)
	h := newHandler(log, fakeDB{}, nil, seg, camp, &deliveryHandler{log: log, pool: pool, src: approvedMessages(camp)})

	call := func(method, path, body string, out any) int {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		if out != nil {
			if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
				t.Fatalf("%s %s: %d %s", method, path, rec.Code, rec.Body)
			}
		}
		return rec.Code
	}

	var created content.View
	if code := call("POST", "/api/campaigns", `{"brief": "Send a 30% discount reminder to customers in Istanbul.",
		"audience": {"name": "Istanbul", "match": "all", "conditions": [{"field": "city", "op": "in", "strings": ["Istanbul"]}]}}`, &created); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	id := created.Campaign.ID
	path := fmt.Sprintf("/api/campaigns/%d", id)
	if code := call("POST", path+"/generate", `{}`, nil); code != http.StatusOK {
		t.Fatalf("generate: %d", code)
	}

	// Sending requires approval.
	if code := call("POST", path+"/send", ``, nil); code != http.StatusConflict {
		t.Fatalf("send before approval: %d", code)
	}
	if code := call("POST", path+"/approve", `{"version": 1}`, nil); code != http.StatusOK {
		t.Fatalf("approve: %d", code)
	}
	var rep delivery.Report
	// Scheduling: rejected in the past, "scheduled" with a UTC time, cancellable.
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	if code := call("POST", path+"/schedule", `{"send_at": "`+past+`"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("past schedule: %d", code)
	}
	later := time.Now().Add(time.Hour).Format(time.RFC3339)
	if code := call("POST", path+"/schedule", `{"send_at": "`+later+`"}`, &rep); code != http.StatusAccepted || rep.Status != "scheduled" || rep.ScheduledFor == nil || rep.ScheduledFor.Location() != time.UTC {
		t.Fatalf("schedule: %d %+v", code, rep)
	}
	if code := call("DELETE", path+"/schedule", ``, &rep); code != http.StatusOK || rep.Status != "not_sent" {
		t.Fatalf("cancel schedule: %d %+v", code, rep)
	}
	// 10 email + 5 SMS to send; the 5 audience members without SMS consent are recorded as skipped.
	if code := call("POST", path+"/send", ``, &rep); code != http.StatusAccepted || rep.Status != "queued" || rep.Totals.Total != 20 || rep.Totals.Skipped != 5 {
		t.Fatalf("send: %d %+v", code, rep)
	}
	if code := call("POST", path+"/send", ``, &rep); code != http.StatusAccepted || rep.Totals.Total != 20 {
		t.Fatalf("repeated send: %d %+v", code, rep)
	}

	key := func(c int, ch string) string { return fmt.Sprintf("campaign-%d/customer-%d/%s", id, c, ch) }
	script := map[string]delivery.Fault{key(2, "sms"): delivery.FaultServer, key(3, "email"): delivery.FaultReject}
	providers := map[string]delivery.Provider{}
	for _, ch := range []string{"sms", "email"} {
		providers[ch] = &delivery.Mock{Pool: pool, Channel: ch, Faults: func(req delivery.Request, call int) delivery.Fault {
			if call == 1 {
				return script[req.Key]
			}
			return delivery.FaultNone
		}}
	}
	cfg := delivery.DefaultConfig
	cfg.Poll, cfg.Policy = 10*time.Millisecond, delivery.Policy{Base: 20 * time.Millisecond, Max: 50 * time.Millisecond}
	wctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		delivery.NewWorker(pool, providers, cfg, log).Run(wctx)
		close(done)
	}()
	defer func() {
		stop()
		<-done
	}()

	deadline := time.Now().Add(10 * time.Second)
	for !strings.HasPrefix(rep.Status, "completed") {
		if time.Now().After(deadline) {
			t.Fatalf("delivery did not finish: %+v", rep)
		}
		time.Sleep(20 * time.Millisecond)
		call("GET", path+"/delivery", ``, &rep)
	}
	sms, email := rep.Channels["sms"], rep.Channels["email"]
	if rep.Status != "completed_with_failures" || rep.Totals.Sent != 14 || rep.Totals.Failed != 1 || rep.Totals.Retries != 1 ||
		sms.Total != 10 || sms.Sent != 5 || sms.Skipped != 5 || email.Total != 10 || email.Failed != 1 || rep.RetryableFailed != 0 {
		t.Fatalf("report %+v", rep)
	}
	if len(rep.Errors) != 2 || !strings.HasPrefix(rep.Errors[0].Reason, "not eligible: no SMS consent") ||
		!strings.HasPrefix(rep.Errors[1].Reason, "400 EMAIL_REJECTED") {
		t.Fatalf("errors %+v", rep.Errors)
	}

	// Recipient-level details and attempt history.
	var page delivery.MessagePage
	if call("GET", path+"/messages?status=failed", ``, &page); page.Total != 1 || page.Items[0].CustomerID != 3 || !page.Items[0].Permanent {
		t.Fatalf("failed messages %+v", page)
	}
	failed := fmt.Sprintf("%s/messages/%d", path, page.Items[0].ID)
	var detail delivery.MessageDetail
	if call("GET", failed, ``, &detail); len(detail.History) != 1 || *detail.History[0].Code != "EMAIL_REJECTED" || detail.Body == "" {
		t.Fatalf("detail %+v", detail)
	}
	var retried struct{ Error string }
	if code := call("POST", failed+"/retry", ``, &retried); code != http.StatusConflict || !strings.Contains(retried.Error, "permanently rejected") {
		t.Fatalf("retry permanent: %d %+v", code, retried)
	}
	var res delivery.RetryResult
	if code := call("POST", path+"/retry-failed", ``, &res); code != http.StatusOK || res.Requeued != 0 || res.Permanent != 1 {
		t.Fatalf("retry failed: %d %+v", code, res)
	}
	if code := call("GET", path+"/messages?status=bogus", ``, nil); code != http.StatusBadRequest {
		t.Fatalf("bad filter: %d", code)
	}

	var m delivery.Metrics
	if call("GET", "/api/metrics", ``, &m); m.Deliveries["email"].Sent != 9 || m.Queue.Due != 0 || m.Attempts != 16 {
		t.Fatalf("metrics %+v", m)
	}
	var list struct{ Campaigns []delivery.CampaignSummary }
	if call("GET", "/api/campaigns", ``, &list); len(list.Campaigns) != 1 || list.Campaigns[0].DeliveryStatus != "completed_with_failures" || list.Campaigns[0].Sent != 14 {
		t.Fatalf("list %+v", list)
	}
}
