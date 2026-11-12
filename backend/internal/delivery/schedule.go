package delivery

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrAlreadySent  = errors.New("sending has already started")
	ErrPastSchedule = errors.New("the scheduled time must be at least a minute in the future")
)

// MinLeadTime is how far ahead a send may be scheduled.
const MinLeadTime = time.Minute

// Schedule sets (or changes) the send time of an approved campaign that has
// not started sending. The worker dispatches it once due.
func Schedule(ctx context.Context, pool *pgxpool.Pool, campaignID int64, at time.Time) error {
	if at.Before(time.Now().Add(MinLeadTime - 5*time.Second)) { // small tolerance for clock and request latency
		return ErrPastSchedule
	}
	tag, err := pool.Exec(ctx, `UPDATE campaigns SET scheduled_for = $2, schedule_error = NULL, updated_at = now()
		WHERE id = $1 AND status = 'approved' AND dispatched_at IS NULL`, campaignID, at.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return scheduleMiss(ctx, pool, campaignID)
}

// CancelSchedule removes a pending schedule. Once dispatch has started it is
// too late (ErrAlreadySent); cancelling an unscheduled campaign is a no-op.
func CancelSchedule(ctx context.Context, pool *pgxpool.Pool, campaignID int64) error {
	tag, err := pool.Exec(ctx, `UPDATE campaigns SET scheduled_for = NULL, schedule_error = NULL, updated_at = now()
		WHERE id = $1 AND dispatched_at IS NULL`, campaignID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return scheduleMiss(ctx, pool, campaignID)
}

// scheduleMiss explains why a guarded update matched no row.
func scheduleMiss(ctx context.Context, pool *pgxpool.Pool, id int64) error {
	var status string
	var dispatched bool
	err := pool.QueryRow(ctx, `SELECT status, dispatched_at IS NOT NULL FROM campaigns WHERE id = $1`, id).Scan(&status, &dispatched)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return err
	case dispatched:
		return ErrAlreadySent
	default:
		return ErrNotApproved
	}
}

// RunDue dispatches campaigns whose scheduled time has passed. It is safe to
// run from several workers at once: DispatchDue serializes on the campaign
// row and re-checks that the schedule is still due and unsent. Permanent
// problems are recorded on the campaign (and shown to the marketer) instead
// of being retried forever; transient errors are retried on the next tick.
func RunDue(ctx context.Context, pool *pgxpool.Pool, src MessageSource, log *slog.Logger) error {
	rows, err := pool.Query(ctx, `SELECT id FROM campaigns
		WHERE scheduled_for <= now() AND dispatched_at IS NULL AND schedule_error IS NULL
		ORDER BY scheduled_for, id LIMIT 10`)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	for _, id := range ids {
		created, err := DispatchDue(ctx, pool, id, src)
		switch {
		case err == nil:
			if created > 0 {
				log.Info("scheduled campaign dispatched", "campaign", id, "messages", created)
			}
		case errors.Is(err, ErrNoRecipients), errors.Is(err, ErrNotApproved):
			log.Warn("scheduled send could not start", "campaign", id, "error", err)
			if _, e := pool.Exec(ctx, `UPDATE campaigns SET schedule_error = $2, updated_at = now()
				WHERE id = $1 AND dispatched_at IS NULL`, id, err.Error()); e != nil {
				return e
			}
		case ctx.Err() != nil:
			return ctx.Err()
		default:
			log.Error("scheduled dispatch failed; will retry", "campaign", id, "error", err)
		}
	}
	return nil
}

func (w *Worker) runScheduler(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		if err := RunDue(ctx, w.pool, w.source, w.log); err != nil && ctx.Err() == nil {
			w.log.Error("schedule check failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
