package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrNotApproved  = errors.New("the campaign must be approved before it can be sent")
	ErrNoRecipients = errors.New("no one in the audience can currently receive this campaign")
)

// Message is one resolved message to queue: recipient, channel, final text
// and where the text came from (for monitoring breakdowns).
type Message struct {
	CustomerID      int64
	Channel         string // sms | email
	VariantPosition int    // 1-3, 0 = no A/B test
	GroupPosition   *int
	Language        string
	Source          string // template | group | individual | fallback
	Subject         string
	Body            string
	// Skip, if set, records an audience member who cannot receive this
	// channel (e.g. no SMS consent): stored as a skipped delivery, never sent.
	Skip string
}

// MessageSource resolves the approved campaign for its current audience
// (implemented by the content service).
type MessageSource func(ctx context.Context, campaignID int64, fn func(Message) error) error

const insertChunk = 1000

// Dispatch creates the campaign's delivery jobs exactly once. It locks the
// campaign row, so concurrent calls serialize; a campaign that was already
// dispatched is left as is (created = 0). Jobs carry their final text, so
// nothing that happens to drafts later can change what is sent.
func Dispatch(ctx context.Context, pool *pgxpool.Pool, campaignID int64, src MessageSource) (created int, err error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var status string
	var dispatched bool
	err = tx.QueryRow(ctx, `SELECT status, dispatched_at IS NOT NULL FROM campaigns WHERE id = $1 FOR UPDATE`, campaignID).Scan(&status, &dispatched)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, ErrNotFound
	case err != nil:
		return 0, err
	case status != "approved":
		return 0, ErrNotApproved
	case dispatched:
		return 0, nil // idempotent: never recreate or restart a batch
	}

	var msgs []Message
	sendable := 0
	if err := src(ctx, campaignID, func(m Message) error {
		msgs = append(msgs, m)
		if m.Skip == "" {
			sendable++
		}
		return nil
	}); err != nil {
		return 0, fmt.Errorf("resolve messages: %w", err)
	}
	if sendable == 0 {
		return 0, ErrNoRecipients
	}
	for start := 0; start < len(msgs); start += insertChunk {
		n, err := insert(ctx, tx, campaignID, msgs[start:min(start+insertChunk, len(msgs))])
		if err != nil {
			return 0, err
		}
		created += n
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET dispatched_at = now(), updated_at = now() WHERE id = $1`, campaignID); err != nil {
		return 0, err
	}
	return created, tx.Commit(ctx)
}

func insert(ctx context.Context, tx pgx.Tx, campaignID int64, msgs []Message) (int, error) {
	n := len(msgs)
	customers, channels, langs, sources, subjects, bodies := make([]int64, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	variants, groups, statuses, reasons := make([]*int32, n), make([]*int32, n), make([]string, n), make([]*string, n)
	for i, m := range msgs {
		customers[i], channels[i], langs[i], sources[i], subjects[i], bodies[i] = m.CustomerID, m.Channel, m.Language, m.Source, m.Subject, m.Body
		statuses[i] = StatusQueued
		if m.Skip != "" {
			statuses[i], reasons[i] = StatusSkipped, &m.Skip
		}
		if m.VariantPosition > 0 {
			v := int32(m.VariantPosition)
			variants[i] = &v
		}
		if m.GroupPosition != nil {
			g := int32(*m.GroupPosition)
			groups[i] = &g
		}
	}
	// The unique (campaign, customer, channel) constraint is the last line of
	// defense against duplicates.
	tag, err := tx.Exec(ctx, `INSERT INTO deliveries
		(campaign_id, customer_id, channel, variant_position, group_position, language, content_source, subject, body, status, last_error)
		SELECT $1, * FROM unnest($2::bigint[], $3::text[], $4::int[], $5::int[], $6::text[], $7::text[], $8::text[], $9::text[], $10::text[], $11::text[])
		ON CONFLICT (campaign_id, customer_id, channel) DO NOTHING`,
		campaignID, customers, channels, variants, groups, langs, sources, subjects, bodies, statuses, reasons)
	return int(tag.RowsAffected()), err
}

// Job is a claimed delivery: everything the worker needs to send it.
type Job struct {
	ID         int64
	CampaignID int64
	CustomerID int64
	Channel    string
	Subject    string
	Body       string
	Attempts   int // including the current one
	RetryBase  int // attempts made before the last manual retry
}

// Round is the attempt number within the current round (manual retries start a new one).
func (j Job) Round() int { return j.Attempts - j.RetryBase }

// Key is the idempotency key sent to the provider: one per recipient,
// campaign and channel (the same identity as the uniqueness constraint), and
// never changed on retry.
func (j Job) Key() string {
	return fmt.Sprintf("campaign-%d/customer-%d/%s", j.CampaignID, j.CustomerID, j.Channel)
}

// Claim takes up to n due jobs of a channel and leases them for lease. Due
// means queued or retrying with next_attempt_at reached, or "sending" with
// an expired lease (its worker crashed): that attempt is resumed under the
// same key and not counted again. SKIP LOCKED lets concurrent claimers take
// disjoint jobs; the statement commits immediately, so no transaction stays
// open while the provider is called.
func Claim(ctx context.Context, pool *pgxpool.Pool, channel string, n int, lease time.Duration) ([]Job, error) {
	rows, err := pool.Query(ctx, `UPDATE deliveries d
		SET status = 'sending', attempts = d.attempts + CASE WHEN d.status = 'sending' THEN 0 ELSE 1 END,
		    locked_until = now() + make_interval(secs => $3), updated_at = now()
		FROM (SELECT id FROM deliveries
		      WHERE channel = $1 AND ((status IN ('queued', 'retrying') AND next_attempt_at <= now())
		                              OR (status = 'sending' AND locked_until < now()))
		      ORDER BY next_attempt_at, id LIMIT $2 FOR UPDATE SKIP LOCKED) due
		WHERE d.id = due.id
		RETURNING d.id, d.campaign_id, d.customer_id, d.channel, d.subject, d.body, d.attempts, d.retry_base`,
		channel, n, lease.Seconds())
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Job, error) {
		var j Job
		err := r.Scan(&j.ID, &j.CampaignID, &j.CustomerID, &j.Channel, &j.Subject, &j.Body, &j.Attempts, &j.RetryBase)
		return j, err
	})
}

// Attempt is one provider call, kept in delivery_attempts.
type Attempt struct {
	StartedAt time.Time
	Latency   time.Duration
	Status    int // 0: no response
	MessageID string
}

// Complete records the outcome of a claimed job and, when the provider was
// called (a != nil), the attempt — in one statement. The status guard makes
// a late outcome write from a worker whose lease expired (and whose job was
// finished elsewhere) harmless; the attempt itself is still recorded.
func Complete(ctx context.Context, pool *pgxpool.Pool, j Job, o Outcome, a *Attempt) error {
	var messageID string
	if a != nil {
		messageID = a.MessageID
	}
	_, err := pool.Exec(ctx, `WITH upd AS (
		UPDATE deliveries SET status = $2,
		    last_error = COALESCE(NULLIF($3, ''), last_error),
		    last_code = COALESCE(NULLIF($6, ''), last_code),
		    permanent = $7,
		    next_attempt_at = CASE WHEN $2 = 'retrying' THEN now() + make_interval(secs => $4) ELSE next_attempt_at END,
		    sent_at = CASE WHEN $2 = 'sent' THEN now() END,
		    provider_message_id = COALESCE(NULLIF($5, ''), provider_message_id),
		    locked_until = NULL, updated_at = now()
		WHERE id = $1 AND status = 'sending'
		RETURNING id)
		INSERT INTO delivery_attempts (delivery_id, attempt, started_at, latency_ms, http_status, code, outcome, error, message_id, manual)
		SELECT $1, $8, $9, $10, NULLIF($11, 0), NULLIF($6, ''), $2, NULLIF($3, ''), NULLIF($5, ''), $12
		WHERE $13`,
		j.ID, o.Status, o.Error, o.Delay.Seconds(), messageID, o.Code, o.Permanent,
		j.Attempts, attemptField(a).StartedAt, attemptField(a).Latency.Milliseconds(), attemptField(a).Status,
		j.RetryBase > 0 && j.Round() == 1, a != nil)
	return err
}

func attemptField(a *Attempt) Attempt {
	if a == nil {
		return Attempt{StartedAt: time.Now()}
	}
	return *a
}

// RetryResult is the effect of a manual retry.
type RetryResult struct {
	Requeued  int `json:"requeued"`
	Permanent int `json:"permanent"` // rejected by the provider: not retried
}

// NotRetryableError explains why a single delivery cannot be retried.
type NotRetryableError struct{ Reason string }

func (e *NotRetryableError) Error() string { return e.Reason }

// RetryFailed requeues failed deliveries of a campaign (deliveryID 0 = all)
// that did not fail permanently. It reuses the same rows, idempotency keys,
// worker and retry policy: each gets a new round of up to MaxAttempts
// attempts, and consent is checked again before sending. Permanent
// rejections, accepted (sent), skipped and pending deliveries are untouched.
func RetryFailed(ctx context.Context, pool *pgxpool.Pool, campaignID, deliveryID int64) (RetryResult, error) {
	var r RetryResult
	err := pool.QueryRow(ctx, `WITH req AS (
			UPDATE deliveries SET status = 'retrying', retry_base = attempts, next_attempt_at = now(), updated_at = now()
			WHERE campaign_id = $1 AND ($2 = 0 OR id = $2) AND status = 'failed' AND NOT permanent
			RETURNING id)
		SELECT (SELECT count(*) FROM req),
		       (SELECT count(*) FROM deliveries WHERE campaign_id = $1 AND ($2 = 0 OR id = $2) AND status = 'failed' AND permanent)`,
		campaignID, deliveryID).Scan(&r.Requeued, &r.Permanent)
	if err != nil {
		return r, err
	}
	if deliveryID != 0 && r.Requeued == 0 {
		var status string
		var permanent bool
		err := pool.QueryRow(ctx, `SELECT status, permanent FROM deliveries WHERE campaign_id = $1 AND id = $2`, campaignID, deliveryID).Scan(&status, &permanent)
		if errors.Is(err, pgx.ErrNoRows) {
			return r, ErrNotFound
		}
		if err != nil {
			return r, err
		}
		reason := map[string]string{
			StatusSent:    "already accepted by the provider; it is not sent again",
			StatusSkipped: "skipped because the recipient cannot receive this channel",
		}[status]
		switch {
		case permanent:
			reason = "permanently rejected by the provider; retrying cannot succeed"
		case reason == "":
			reason = "still in progress"
		}
		return r, &NotRetryableError{Reason: reason}
	}
	return r, nil
}
