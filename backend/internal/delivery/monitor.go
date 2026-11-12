package delivery

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Counts are delivery records by status. Retries are attempts beyond the first.
type Counts struct {
	Total    int `json:"total"`
	Queued   int `json:"queued"`
	Sending  int `json:"sending"`
	Retrying int `json:"retrying"`
	Sent     int `json:"sent"`
	Failed   int `json:"failed"`
	Skipped  int `json:"skipped"`
	Retries  int `json:"retries"`
}

func (c *Counts) add(status string, n, retries int) {
	c.Total += n
	c.Retries += retries
	switch status {
	case StatusQueued:
		c.Queued += n
	case StatusSending:
		c.Sending += n
	case StatusRetrying:
		c.Retrying += n
	case StatusSent:
		c.Sent += n
	case StatusFailed:
		c.Failed += n
	case StatusSkipped:
		c.Skipped += n
	}
}

func (c Counts) pending() int { return c.Queued + c.Sending + c.Retrying }

// BucketCounts are the counts of one A/B variant or behavioral group.
type BucketCounts struct {
	Position int `json:"position"`
	Counts
}

type ErrorCount struct {
	Status string `json:"status"` // retrying | failed | skipped
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// Report is a campaign's delivery state, read entirely from PostgreSQL.
type Report struct {
	CampaignID   int64      `json:"campaign_id"`
	Status       string     `json:"status"` // not_sent | scheduled | queued | sending | completed | completed_with_failures
	DispatchedAt *time.Time `json:"dispatched_at"`
	// ScheduledFor (UTC) is set while a send is scheduled and not yet started.
	ScheduledFor  *time.Time        `json:"scheduled_for"`
	ScheduleError *string           `json:"schedule_error"`
	FinishedAt    *time.Time        `json:"finished_at"`
	Totals        Counts            `json:"totals"`
	Channels      map[string]Counts `json:"channels"`
	Variants      []BucketCounts    `json:"variants"`
	Groups        []BucketCounts    `json:"groups"`
	Errors        []ErrorCount      `json:"errors"`
	NextRetryAt   *time.Time        `json:"next_retry_at"`
	WorkerSeenAt  *time.Time        `json:"worker_seen_at"`
	// RetryableFailed are failed deliveries a marketer can retry (not permanent rejections).
	RetryableFailed int `json:"retryable_failed"`
}

func status(dispatched bool, c Counts) string {
	switch {
	case !dispatched:
		return "not_sent"
	case c.pending() > 0 && c.Sending+c.Retrying+c.Sent+c.Failed == 0: // nothing attempted yet (skips excepted)
		return "queued"
	case c.pending() > 0:
		return "sending"
	case c.Failed > 0:
		return "completed_with_failures"
	default:
		return "completed"
	}
}

// CampaignReport builds the monitoring view of one campaign.
func CampaignReport(ctx context.Context, pool *pgxpool.Pool, campaignID int64) (Report, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r := Report{CampaignID: campaignID, Channels: map[string]Counts{}, Variants: []BucketCounts{}, Groups: []BucketCounts{}, Errors: []ErrorCount{}}
	var lastUpdate *time.Time
	err := pool.QueryRow(ctx, `SELECT c.dispatched_at, c.scheduled_for, c.schedule_error,
			(SELECT max(updated_at) FROM deliveries WHERE campaign_id = c.id),
			(SELECT min(next_attempt_at) FROM deliveries WHERE campaign_id = c.id AND status = 'retrying'),
			(SELECT max(seen_at) FROM worker_heartbeats),
			(SELECT count(*) FROM deliveries WHERE campaign_id = c.id AND status = 'failed' AND NOT permanent)
		FROM campaigns c WHERE c.id = $1`, campaignID).Scan(&r.DispatchedAt, &r.ScheduledFor, &r.ScheduleError, &lastUpdate, &r.NextRetryAt, &r.WorkerSeenAt, &r.RetryableFailed)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}

	rows, err := Breakdown(ctx, pool, campaignID)
	if err != nil {
		return r, err
	}
	variants, groups := map[int]*BucketCounts{}, map[int]*BucketCounts{}
	bucket := func(m map[int]*BucketCounts, pos int) *BucketCounts {
		if m[pos] == nil {
			m[pos] = &BucketCounts{Position: pos}
		}
		return m[pos]
	}
	for _, row := range rows {
		r.Totals.add(row.Status, row.Messages, row.Retries)
		ch := r.Channels[row.Channel]
		ch.add(row.Status, row.Messages, row.Retries)
		r.Channels[row.Channel] = ch
		if row.VariantPosition != nil {
			bucket(variants, *row.VariantPosition).add(row.Status, row.Messages, row.Retries)
		}
		if row.GroupPosition != nil {
			bucket(groups, *row.GroupPosition).add(row.Status, row.Messages, row.Retries)
		}
	}
	r.Variants, r.Groups = sorted(variants), sorted(groups)
	r.Status = status(r.DispatchedAt != nil, r.Totals)
	if r.ScheduledFor != nil {
		u := r.ScheduledFor.UTC()
		r.ScheduledFor = &u
		if r.Status == "not_sent" {
			r.Status = "scheduled"
		}
	}
	if r.Status == "completed" || r.Status == "completed_with_failures" {
		r.FinishedAt = lastUpdate
	}

	errRows, err := pool.Query(ctx, `SELECT status, last_error, count(*) FROM deliveries
		WHERE campaign_id = $1 AND status IN ('retrying', 'failed', 'skipped') AND last_error IS NOT NULL
		GROUP BY status, last_error ORDER BY count(*) DESC, status, last_error LIMIT 8`, campaignID)
	if err != nil {
		return r, err
	}
	r.Errors, err = pgx.CollectRows(errRows, func(row pgx.CollectableRow) (ErrorCount, error) {
		var e ErrorCount
		return e, row.Scan(&e.Status, &e.Reason, &e.Count)
	})
	return r, err
}

func sorted(m map[int]*BucketCounts) []BucketCounts {
	out := []BucketCounts{}
	for _, b := range m {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out
}

// Metrics are global queue and delivery statistics.
type Metrics struct {
	Queue struct {
		Due              int     `json:"due"`               // ready to be claimed now
		ScheduledRetries int     `json:"scheduled_retries"` // waiting for their backoff
		InFlight         int     `json:"in_flight"`         // claimed (sending)
		OldestDueSeconds float64 `json:"oldest_due_seconds"`
	} `json:"queue"`
	Deliveries     map[string]Counts `json:"deliveries"`       // per channel
	SentLastMinute map[string]int    `json:"sent_last_minute"` // per channel
	Attempts       int               `json:"attempts"`
	WorkerSeenAt   *time.Time        `json:"worker_seen_at"`
}

func GetMetrics(ctx context.Context, pool *pgxpool.Pool) (Metrics, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	m := Metrics{Deliveries: map[string]Counts{}, SentLastMinute: map[string]int{}}
	q := &m.Queue
	err := pool.QueryRow(ctx, `SELECT
			count(*) FILTER (WHERE status IN ('queued', 'retrying') AND next_attempt_at <= now()),
			count(*) FILTER (WHERE status = 'retrying' AND next_attempt_at > now()),
			count(*) FILTER (WHERE status = 'sending'),
			COALESCE(EXTRACT(EPOCH FROM now() - min(next_attempt_at) FILTER (WHERE status IN ('queued', 'retrying') AND next_attempt_at <= now())), 0)::float8,
			(SELECT max(seen_at) FROM worker_heartbeats)
		FROM deliveries`).Scan(&q.Due, &q.ScheduledRetries, &q.InFlight, &q.OldestDueSeconds, &m.WorkerSeenAt)
	if err != nil {
		return m, err
	}
	rows, err := pool.Query(ctx, `SELECT channel, status, count(*), COALESCE(sum(attempts), 0), COALESCE(sum(GREATEST(attempts - 1, 0)), 0),
			count(*) FILTER (WHERE sent_at > now() - interval '1 minute')
		FROM deliveries GROUP BY channel, status`)
	if err != nil {
		return m, err
	}
	defer rows.Close()
	for rows.Next() {
		var channel, st string
		var n, attempts, retries, recent int
		if err := rows.Scan(&channel, &st, &n, &attempts, &retries, &recent); err != nil {
			return m, err
		}
		c := m.Deliveries[channel]
		c.add(st, n, retries)
		m.Deliveries[channel] = c
		m.SentLastMinute[channel] += recent
		m.Attempts += attempts
	}
	return m, rows.Err()
}

// CampaignSummary is one row of the recent campaigns list.
type CampaignSummary struct {
	ID             int64      `json:"id"`
	CreatedAt      time.Time  `json:"created_at"`
	Brief          string     `json:"brief"`
	Audience       string     `json:"audience"`
	Status         string     `json:"status"` // draft | approved
	DispatchedAt   *time.Time `json:"dispatched_at"`
	ScheduledFor   *time.Time `json:"scheduled_for"`
	DeliveryStatus string     `json:"delivery_status"`
	Messages       int        `json:"messages"`
	Sent           int        `json:"sent"`
	Failed         int        `json:"failed"`
}

// RecentCampaigns lists the latest campaigns with their delivery progress.
func RecentCampaigns(ctx context.Context, pool *pgxpool.Pool, limit int) ([]CampaignSummary, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := pool.Query(ctx, `SELECT c.id, c.created_at, c.brief, COALESCE(c.audience->>'name', ''), c.status, c.dispatched_at, c.scheduled_for,
			count(d.id), count(d.id) FILTER (WHERE d.status = 'queued'), count(d.id) FILTER (WHERE d.status IN ('sending', 'retrying')),
			count(d.id) FILTER (WHERE d.status = 'sent'), count(d.id) FILTER (WHERE d.status = 'failed'),
			COALESCE(sum(GREATEST(d.attempts - 1, 0)), 0)
		FROM campaigns c LEFT JOIN deliveries d ON d.campaign_id = c.id
		GROUP BY c.id ORDER BY c.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (CampaignSummary, error) {
		var s CampaignSummary
		var c Counts
		var active int
		err := row.Scan(&s.ID, &s.CreatedAt, &s.Brief, &s.Audience, &s.Status, &s.DispatchedAt, &s.ScheduledFor,
			&c.Total, &c.Queued, &active, &c.Sent, &c.Failed, &c.Retries)
		c.Sending = active // only the pending total matters for the status
		s.Messages, s.Sent, s.Failed = c.Total, c.Sent, c.Failed
		s.DeliveryStatus = status(s.DispatchedAt != nil, c)
		if s.ScheduledFor != nil {
			u := s.ScheduledFor.UTC()
			s.ScheduledFor = &u
			if s.DeliveryStatus == "not_sent" {
				s.DeliveryStatus = "scheduled"
			}
		}
		return s, err
	})
}

// MessageRow is one delivery (recipient and channel) in the details table.
type MessageRow struct {
	ID              int64      `json:"id"`
	CustomerID      int64      `json:"customer_id"`
	Name            string     `json:"name"`
	To              string     `json:"to"` // masked current address
	Channel         string     `json:"channel"`
	VariantPosition *int       `json:"variant_position"`
	GroupPosition   *int       `json:"group_position"`
	Language        string     `json:"language"`
	Status          string     `json:"status"`
	Attempts        int        `json:"attempts"`
	LastCode        *string    `json:"last_code"`
	LastError       *string    `json:"last_error"`
	Permanent       bool       `json:"permanent"`
	Retryable       bool       `json:"retryable"`
	NextAttemptAt   *time.Time `json:"next_attempt_at"` // retrying only
	SentAt          *time.Time `json:"sent_at"`
	MessageID       *string    `json:"provider_message_id"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type MessagePage struct {
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
	Items    []MessageRow `json:"items"`
}

// MessageFilter selects deliveries. Status "pending" means queued or sending.
type MessageFilter struct {
	Status, Channel string
	Page, PageSize  int
}

const messageCols = `d.id, d.customer_id, COALESCE(c.first_name || ' ' || c.last_name, 'deleted customer'),
	CASE WHEN d.channel = 'sms' THEN COALESCE(c.phone, '') ELSE COALESCE(c.email, '') END,
	d.channel, d.variant_position, d.group_position, d.language, d.status, d.attempts, d.last_code, d.last_error, d.permanent,
	CASE WHEN d.status = 'retrying' THEN d.next_attempt_at END, d.sent_at, d.provider_message_id, d.updated_at`

func scanMessage(row pgx.CollectableRow) (MessageRow, error) {
	var m MessageRow
	err := row.Scan(&m.ID, &m.CustomerID, &m.Name, &m.To, &m.Channel, &m.VariantPosition, &m.GroupPosition, &m.Language,
		&m.Status, &m.Attempts, &m.LastCode, &m.LastError, &m.Permanent, &m.NextAttemptAt, &m.SentAt, &m.MessageID, &m.UpdatedAt)
	m.To = mask(m.To)
	m.Retryable = m.Status == StatusFailed && !m.Permanent
	return m, err
}

// Messages lists a campaign's deliveries, one page at a time, by id.
func Messages(ctx context.Context, pool *pgxpool.Pool, campaignID int64, f MessageFilter) (MessagePage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p := MessagePage{Page: max(f.Page, 1), PageSize: min(max(f.PageSize, 1), 100), Items: []MessageRow{}}
	statuses := []string{f.Status}
	switch f.Status {
	case "":
		statuses = []string{} // not nil: a NULL array would match nothing
	case "pending":
		statuses = []string{StatusQueued, StatusSending}
	}
	where := `d.campaign_id = $1 AND (cardinality($2::text[]) = 0 OR d.status = ANY($2)) AND ($3 = '' OR d.channel = $3)`
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM deliveries d WHERE `+where, campaignID, statuses, f.Channel).Scan(&p.Total); err != nil {
		return p, err
	}
	rows, err := pool.Query(ctx, `SELECT `+messageCols+` FROM deliveries d LEFT JOIN customers c ON c.id = d.customer_id
		WHERE `+where+` ORDER BY d.id LIMIT $4 OFFSET $5`, campaignID, statuses, f.Channel, p.PageSize, (p.Page-1)*p.PageSize)
	if err != nil {
		return p, err
	}
	p.Items, err = pgx.CollectRows(rows, scanMessage)
	return p, err
}

// AttemptRow is one provider call of a delivery.
type AttemptRow struct {
	Attempt    int       `json:"attempt"`
	StartedAt  time.Time `json:"started_at"`
	LatencyMS  int       `json:"latency_ms"`
	HTTPStatus *int      `json:"http_status"` // null: no response
	Code       *string   `json:"code"`
	Outcome    string    `json:"outcome"`
	Error      *string   `json:"error"`
	MessageID  *string   `json:"message_id"`
	Manual     bool      `json:"manual"` // first attempt after a manual retry
}

type MessageDetail struct {
	MessageRow
	Subject string       `json:"subject"`
	Body    string       `json:"body"`
	History []AttemptRow `json:"attempts_history"`
}

// GetMessage returns one delivery with its content and attempt history.
func GetMessage(ctx context.Context, pool *pgxpool.Pool, campaignID, id int64) (MessageDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var d MessageDetail
	rows, err := pool.Query(ctx, `SELECT `+messageCols+`, d.subject, d.body FROM deliveries d LEFT JOIN customers c ON c.id = d.customer_id
		WHERE d.campaign_id = $1 AND d.id = $2`, campaignID, id)
	if err != nil {
		return d, err
	}
	d, err = pgx.CollectExactlyOneRow(rows, func(row pgx.CollectableRow) (MessageDetail, error) {
		var d MessageDetail
		m := &d.MessageRow
		err := row.Scan(&m.ID, &m.CustomerID, &m.Name, &m.To, &m.Channel, &m.VariantPosition, &m.GroupPosition, &m.Language,
			&m.Status, &m.Attempts, &m.LastCode, &m.LastError, &m.Permanent, &m.NextAttemptAt, &m.SentAt, &m.MessageID, &m.UpdatedAt,
			&d.Subject, &d.Body)
		m.To = mask(m.To)
		m.Retryable = m.Status == StatusFailed && !m.Permanent
		return d, err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	hist, err := pool.Query(ctx, `SELECT attempt, started_at, latency_ms, http_status, code, outcome, error, message_id, manual
		FROM delivery_attempts WHERE delivery_id = $1 ORDER BY id`, id)
	if err != nil {
		return d, err
	}
	d.History, err = pgx.CollectRows(hist, func(row pgx.CollectableRow) (AttemptRow, error) {
		var a AttemptRow
		return a, row.Scan(&a.Attempt, &a.StartedAt, &a.LatencyMS, &a.HTTPStatus, &a.Code, &a.Outcome, &a.Error, &a.MessageID, &a.Manual)
	})
	return d, err
}

// mask shows enough of an address to recognize it: "a•••@example.com", "•••4567".
func mask(addr string) string {
	if at := strings.IndexByte(addr, '@'); at > 0 {
		return addr[:1] + "•••" + addr[at:]
	}
	if len(addr) > 4 {
		return "•••" + addr[len(addr)-4:]
	}
	return addr
}
