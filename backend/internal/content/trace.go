package content

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/smart-campaign-composer/backend/internal/llm"
)

const maxRawOutput = 32 << 10

// run is one traced model call; see content_runs in schema.sql. Every call
// (draft, localize, groups, group_content, individual) is recorded, which is
// also what actual AI cost is computed from.
type run struct {
	campaignID                int64
	kind                      string
	fromVersion, draftVersion int
	customerID                int64
	groupPosition             *int
	source, model             string
	status, errMsg            string
	llmMs, totalMs            int
	inputTokens, outputTokens int
	input                     map[string]any
	rawOutput                 string
	start                     time.Time
}

// finish records the model call's usage and timing.
func (r *run) finish(start time.Time, u Usage, err error) {
	r.start = start
	r.llmMs = int(time.Since(start).Milliseconds())
	r.rawOutput, r.inputTokens, r.outputTokens = u.Raw, u.InputTokens, u.OutputTokens
	if u.Model != "" {
		r.model = u.Model
	}
	r.status = statusOf(err)
	if err != nil {
		r.errMsg = err.Error()
	}
}

// fail marks the run failed after the call (e.g. storing the result failed).
func (r *run) fail(err error) {
	r.status, r.errMsg = statusOf(err), err.Error()
}

func statusOf(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, llm.ErrRefusal):
		return "refusal"
	case errors.Is(err, llm.ErrAuth):
		return "auth_error"
	case errors.Is(err, llm.ErrRetry), errors.Is(err, context.DeadlineExceeded):
		return "unavailable"
	case errors.Is(err, llm.ErrBadReply):
		return "bad_reply"
	default:
		return "error"
	}
}

// record stores the trace. It never fails the request and detaches from the
// request context, so a client abort still leaves a trace.
func (s *Service) record(ctx context.Context, r run) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if len(r.rawOutput) > maxRawOutput {
		r.rawOutput = r.rawOutput[:maxRawOutput]
	}
	if r.status == "" {
		r.status = "ok"
	}
	if !r.start.IsZero() {
		r.totalMs = int(time.Since(r.start).Milliseconds())
	}
	if r.input == nil {
		r.input = map[string]any{}
	}
	input, _ := json.Marshal(r.input)
	_, err := s.pool.Exec(ctx, `INSERT INTO content_runs
		(campaign_id, kind, from_version, draft_version, customer_id, group_position, source, model, status, error,
		 llm_ms, total_ms, input_tokens, output_tokens, input, raw_output)
		VALUES ($1, $2, NULLIF($3,0), NULLIF($4,0), NULLIF($5,0), $6, $7, NULLIF($8,''), $9, NULLIF($10,''),
		        NULLIF($11,0), $12, NULLIF($13,0), NULLIF($14,0), $15, NULLIF($16,''))`,
		r.campaignID, r.kind, r.fromVersion, r.draftVersion, r.customerID, r.groupPosition, r.source, r.model, r.status, r.errMsg,
		r.llmMs, r.totalMs, r.inputTokens, r.outputTokens, input, r.rawOutput)
	if err != nil {
		s.log.Warn("could not record content run", "error", err)
	}
}
