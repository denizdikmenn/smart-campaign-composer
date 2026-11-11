// Package delivery holds the delivery-record contract shared by the
// (upcoming) delivery pipeline and campaign monitoring. Each record carries
// the resolved content and its origin (variant, behavioral group, language,
// content source), assigned once when the record is created; retries only
// change status and attempts, so monitoring breakdowns stay accurate.
package delivery

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Row is one bucket of delivery records: a variant and/or group, a channel
// and a status. Counts are delivery performance only (no opens, clicks or
// conversions exist in this system).
type Row struct {
	VariantPosition *int   `json:"variant_position"`
	GroupPosition   *int   `json:"group_position"`
	Channel         string `json:"channel"`
	Status          string `json:"status"`
	Messages        int    `json:"messages"`
	Recipients      int    `json:"recipients"`
	Retries         int    `json:"retries"` // attempts beyond the first
}

// Breakdown groups a campaign's delivery records by variant, group, channel
// and status.
func Breakdown(ctx context.Context, pool *pgxpool.Pool, campaignID int64) ([]Row, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := pool.Query(ctx, `SELECT variant_position, group_position, channel, status,
		count(*), count(DISTINCT customer_id), COALESCE(sum(GREATEST(attempts - 1, 0)), 0)
		FROM deliveries WHERE campaign_id = $1
		GROUP BY variant_position, group_position, channel, status
		ORDER BY variant_position NULLS FIRST, group_position NULLS FIRST, channel, status`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		if err := rows.Scan(&r.VariantPosition, &r.GroupPosition, &r.Channel, &r.Status, &r.Messages, &r.Recipients, &r.Retries); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
