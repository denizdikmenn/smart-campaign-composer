package delivery

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestScheduledSendWaitsUntilDueThenRunsOnce(t *testing.T) {
	ctx, pool := setup(t, 10)
	src := source(10, "sms", "email")

	if err := Schedule(ctx, pool, 1, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rep, _ := CampaignReport(ctx, pool, 1)
	if rep.Status != "scheduled" || rep.ScheduledFor == nil {
		t.Fatalf("want scheduled, got %q", rep.Status)
	}
	if err := RunDue(ctx, pool, src, quiet); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, `SELECT count(*) FROM deliveries WHERE campaign_id = 1`); n != 0 {
		t.Fatalf("not due yet, but %d deliveries exist", n)
	}

	pool.Exec(ctx, `UPDATE campaigns SET scheduled_for = now() - interval '1 second' WHERE id = 1`)
	var wg sync.WaitGroup
	for range 5 { // several workers racing on the same due campaign
		wg.Add(1)
		go func() { defer wg.Done(); RunDue(ctx, pool, src, quiet) }()
	}
	wg.Wait()
	if n := count(t, pool, `SELECT count(*) FROM deliveries WHERE campaign_id = 1`); n != 20 {
		t.Fatalf("want exactly 20 deliveries, got %d", n)
	}
	if rep, _ = CampaignReport(ctx, pool, 1); rep.Status != "queued" || rep.ScheduledFor != nil {
		t.Fatalf("after dispatch want queued and no schedule, got %q %v", rep.Status, rep.ScheduledFor)
	}
	RunDue(ctx, pool, src, quiet)
	if n := count(t, pool, `SELECT count(*) FROM deliveries WHERE campaign_id = 1`); n != 20 {
		t.Fatalf("second pass duplicated: %d", n)
	}
}

func TestCancelledScheduleNeverSends(t *testing.T) {
	ctx, pool := setup(t, 5)
	src := source(5, "sms")
	if err := Schedule(ctx, pool, 1, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, `UPDATE campaigns SET scheduled_for = now() - interval '1 second' WHERE id = 1`)
	if err := CancelSchedule(ctx, pool, 1); err != nil {
		t.Fatal(err)
	}
	// Even a dispatcher that picked the campaign before the cancel does nothing.
	if n, err := DispatchDue(ctx, pool, 1, src); n != 0 || err != nil {
		t.Fatalf("cancelled schedule dispatched: %d, %v", n, err)
	}
	RunDue(ctx, pool, src, quiet)
	if n := count(t, pool, `SELECT count(*) FROM deliveries WHERE campaign_id = 1`); n != 0 {
		t.Fatalf("got %d deliveries", n)
	}

	// Too late once sending has started.
	if _, err := Dispatch(ctx, pool, 1, src); err != nil {
		t.Fatal(err)
	}
	if err := CancelSchedule(ctx, pool, 1); !errors.Is(err, ErrAlreadySent) {
		t.Fatalf("want ErrAlreadySent, got %v", err)
	}
	if err := Schedule(ctx, pool, 1, time.Now().Add(time.Hour)); !errors.Is(err, ErrAlreadySent) {
		t.Fatalf("want ErrAlreadySent, got %v", err)
	}
}

func TestScheduleValidation(t *testing.T) {
	ctx, pool := setup(t, 1)
	if err := Schedule(ctx, pool, 1, time.Now().Add(-time.Hour)); !errors.Is(err, ErrPastSchedule) {
		t.Fatalf("past: %v", err)
	}
	if err := Schedule(ctx, pool, 3, time.Now().Add(time.Hour)); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("draft: %v", err)
	}
	if err := Schedule(ctx, pool, 99, time.Now().Add(time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestScheduledSendWithNoRecipientsIsNotRetried(t *testing.T) {
	ctx, pool := setup(t, 3)
	none := MessageSource(func(_ context.Context, _ int64, _ func(Message) error) error { return nil })
	Schedule(ctx, pool, 1, time.Now().Add(time.Hour))
	pool.Exec(ctx, `UPDATE campaigns SET scheduled_for = now() - interval '1 second' WHERE id = 1`)
	RunDue(ctx, pool, none, quiet)
	rep, _ := CampaignReport(ctx, pool, 1)
	if rep.ScheduleError == nil || rep.Status != "scheduled" {
		t.Fatalf("want a recorded schedule error, got %+v", rep)
	}
	if n := count(t, pool, `SELECT count(*) FROM campaigns WHERE scheduled_for <= now() AND dispatched_at IS NULL AND schedule_error IS NULL`); n != 0 {
		t.Fatal("failed schedule would be retried forever")
	}
}
