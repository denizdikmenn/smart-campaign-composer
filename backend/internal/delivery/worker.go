package delivery

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config tunes the worker. Rate applies per channel and to every provider
// attempt, retries included.
type Config struct {
	Name        string        // heartbeat name
	Rate        int           // max provider calls per second, per channel
	Concurrency int           // in-flight sends per channel
	Lease       time.Duration // how long a claimed job is reserved
	Poll        time.Duration // idle wait when nothing is due
	SendTimeout time.Duration // per provider call
	Policy      Policy
}

var DefaultConfig = Config{Name: "worker", Rate: 100, Concurrency: 10, Lease: 30 * time.Second,
	Poll: 250 * time.Millisecond, SendTimeout: 5 * time.Second, Policy: DefaultPolicy}

// Worker sends claimed jobs through the channel's provider and records the
// outcome. It knows nothing about segmentation or content: a job is already
// a resolved message.
type Worker struct {
	pool      *pgxpool.Pool
	providers map[string]Provider
	cfg       Config
	log       *slog.Logger
}

func NewWorker(pool *pgxpool.Pool, providers map[string]Provider, cfg Config, log *slog.Logger) *Worker {
	return &Worker{pool: pool, providers: providers, cfg: cfg, log: log}
}

// Run processes jobs until ctx is cancelled, then lets in-flight sends
// finish. Jobs claimed but not started stay leased and are picked up again
// after the lease expires.
func (w *Worker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for channel, p := range w.providers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.runChannel(ctx, channel, p, newLimiter(w.cfg.Rate, time.Second))
		}()
	}
	w.heartbeat()
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case <-t.C:
			w.heartbeat()
		}
	}
}

func (w *Worker) heartbeat() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := w.pool.Exec(ctx, `INSERT INTO worker_heartbeats (name, seen_at) VALUES ($1, now())
		ON CONFLICT (name) DO UPDATE SET seen_at = now()`, w.cfg.Name); err != nil {
		w.log.Warn("heartbeat", "error", err)
	}
}

// runChannel claims due jobs and hands them to a fixed pool of senders.
func (w *Worker) runChannel(ctx context.Context, channel string, p Provider, lim *limiter) {
	jobs := make(chan Job)
	var wg sync.WaitGroup
	for range w.cfg.Concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				w.process(j, p, lim)
			}
		}()
	}
	defer func() {
		close(jobs)
		wg.Wait()
	}()
	for ctx.Err() == nil {
		batch, err := Claim(ctx, w.pool, channel, w.cfg.Concurrency, w.cfg.Lease)
		if err != nil && ctx.Err() == nil {
			w.log.Error("claim", "channel", channel, "error", err)
		}
		if len(batch) == 0 {
			sleep(ctx, w.cfg.Poll)
			continue
		}
		for _, j := range batch {
			select {
			case jobs <- j:
			case <-ctx.Done():
				return
			}
		}
	}
}

// process sends one job. It runs detached from shutdown so a provider call
// in flight is completed and recorded.
func (w *Worker) process(j Job, p Provider, lim *limiter) {
	ctx, cancel := context.WithTimeout(context.Background(), w.cfg.SendTimeout+10*time.Second)
	defer cancel()

	// Consent can be withdrawn after the job was queued: re-check right before the call.
	to, reason, err := recipient(ctx, w.pool, j)
	if err != nil {
		w.log.Error("recipient lookup", "delivery", j.ID, "error", err)
		return // lease expires, the job is retried later
	}
	if reason != "" {
		w.complete(ctx, j, Outcome{Status: StatusSkipped, Error: reason}, nil)
		return
	}

	if err := lim.Wait(ctx); err != nil {
		return
	}
	sendCtx, cancelSend := context.WithTimeout(ctx, w.cfg.SendTimeout)
	start := time.Now()
	resp, err := p.Send(sendCtx, Request{Key: j.Key(), Channel: j.Channel, To: to, Subject: j.Subject, Body: j.Body})
	a := &Attempt{StartedAt: start, Latency: time.Since(start), Status: resp.Status, MessageID: resp.MessageID}
	cancelSend()
	if err != nil {
		a.Status = 0 // no response
	}
	o := w.cfg.Policy.Decide(resp, err, j.Round())
	if o.Status != StatusSent {
		w.log.Info("delivery attempt failed", "delivery", j.ID, "channel", j.Channel, "attempt", j.Attempts, "outcome", o.Status, "reason", o.Error)
	}
	w.complete(ctx, j, o, a)
}

func (w *Worker) complete(ctx context.Context, j Job, o Outcome, a *Attempt) {
	if err := Complete(ctx, w.pool, j, o, a); err != nil {
		w.log.Error("record outcome", "delivery", j.ID, "error", err)
	}
}

// recipient returns the current address of the job's customer, or a skip
// reason when the customer can no longer be messaged on this channel.
func recipient(ctx context.Context, pool *pgxpool.Pool, j Job) (to, skip string, err error) {
	var email string
	var phone *string
	var emailOK, smsOK bool
	err = pool.QueryRow(ctx, `SELECT email, phone, email_consent, sms_consent FROM customers WHERE id = $1`, j.CustomerID).
		Scan(&email, &phone, &emailOK, &smsOK)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "customer no longer exists", nil
	}
	if err != nil {
		return "", "", err
	}
	switch {
	case j.Channel == "email" && emailOK:
		return email, "", nil
	case j.Channel == "sms" && smsOK && phone != nil:
		return *phone, "", nil
	}
	return "", j.Channel + " consent withdrawn before sending", nil
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}

// limiter allows at most n calls in any window of length per (a sliding
// window over the last n call times). Waiters queue on the mutex.
type limiter struct {
	mu    sync.Mutex
	per   time.Duration
	times []time.Time // ring of the last n call times
	next  int
	now   func() time.Time
	sleep func(context.Context, time.Duration)
}

func newLimiter(n int, per time.Duration) *limiter {
	return &limiter{per: per, times: make([]time.Time, max(n, 1)), now: time.Now, sleep: sleep}
}

// Wait blocks until a call is allowed and records it.
func (l *limiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	// The oldest of the last n calls must be at least per ago.
	if oldest := l.times[l.next]; !oldest.IsZero() {
		if d := oldest.Add(l.per).Sub(l.now()); d > 0 {
			l.sleep(ctx, d)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	l.times[l.next] = l.now()
	l.next = (l.next + 1) % len(l.times)
	return nil
}
