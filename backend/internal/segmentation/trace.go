package segmentation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const maxRawOutput = 32 << 10

// run is one traced suggest request; see segmentation_runs in schema.sql.
type run struct {
	brief, source, model, status, errMsg string
	llmMs, totalMs                       int
	inputTokens, outputTokens            int
	rawOutput                            string
	alternatives                         []Result
	notes                                []string // technical: validation rejections, normalizations
	outcome                              string
	assessment                           Assessment
}

// statusOf classifies a failed request; successful ones use outcomeStatus.
func statusOf(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrNoValidAlternatives):
		return "no_valid"
	case errors.Is(err, ErrRefusal):
		return "refusal"
	case errors.Is(err, ErrAuth):
		return "auth_error"
	case errors.Is(err, ErrRetry), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "unavailable"
	case errors.Is(err, ErrBadReply):
		return "bad_reply"
	default:
		return "error"
	}
}

// record stores the run and returns its id (0 if it could not be stored).
// Tracing must never fail the request, so errors are only logged. It detaches
// from the request context so a client abort still leaves a trace.
func (s *Service) record(ctx context.Context, r run) int64 {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if len(r.rawOutput) > maxRawOutput {
		r.rawOutput = r.rawOutput[:maxRawOutput]
	}
	alts, _ := json.Marshal(nonNil(r.alternatives))
	warns, _ := json.Marshal(nonNil(r.notes))
	var assess []byte
	if r.outcome != "" {
		assess, _ = json.Marshal(r.assessment)
	}
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO segmentation_runs
		(brief, source, model, status, error, llm_ms, total_ms, input_tokens, output_tokens, raw_output, alternatives, warnings, outcome, assessment)
		VALUES ($1,$2,NULLIF($3,''),$4,NULLIF($5,''),NULLIF($6,0),$7,NULLIF($8,0),NULLIF($9,0),NULLIF($10,''),$11,$12,NULLIF($13,''),$14) RETURNING id`,
		r.brief, r.source, r.model, r.status, r.errMsg, r.llmMs, r.totalMs, r.inputTokens, r.outputTokens, r.rawOutput, alts, warns, r.outcome, assess).Scan(&id)
	if err != nil {
		s.log.Warn("could not record segmentation run", "error", err)
		return 0
	}
	return id
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// Run is one traced request as a pipeline: LLM call -> backend validation ->
// counted result. It is assembled from the stored row when read.
type Run struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Brief     string    `json:"brief"`
	Status    string    `json:"status"`
	Outcome   *string   `json:"outcome"`
	Error     *string   `json:"error"`
	TotalMs   int       `json:"total_ms"`
	Trace     []Step    `json:"trace"`
}

// Step is one stage of the pipeline. Status is ok, failed or skipped.
type Step struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	DurationMs *int   `json:"duration_ms,omitempty"`
	Detail     any    `json:"detail,omitempty"`
}

const runColumns = `id, created_at, brief, source, model, status, error, llm_ms, total_ms,
	input_tokens, output_tokens, raw_output, alternatives, warnings, outcome, assessment`

func scanRun(row interface{ Scan(...any) error }, withRaw bool) (Run, error) {
	var (
		r                    Run
		source               string
		model, raw           *string
		llmMs, inTok, outTok *int
		alts, warns, assess  []byte
	)
	if err := row.Scan(&r.ID, &r.CreatedAt, &r.Brief, &source, &model, &r.Status, &r.Error, &llmMs, &r.TotalMs,
		&inTok, &outTok, &raw, &alts, &warns, &r.Outcome, &assess); err != nil {
		return r, err
	}
	var results []Result
	var notes []string
	_ = json.Unmarshal(alts, &results)
	_ = json.Unmarshal(warns, &notes)

	// Step 1: the LLM call (or mock generator).
	llm := Step{Name: "llm_call", Status: "ok", DurationMs: llmMs}
	if r.Outcome == nil && r.Status != "ok" && r.Status != "no_valid" {
		llm.Status = "failed"
	}
	llmDetail := map[string]any{"source": source, "model": model, "input_tokens": inTok, "output_tokens": outTok}
	if llm.Status == "failed" {
		llmDetail["error"] = r.Error
	}
	if withRaw {
		llmDetail["raw_output"] = raw
	}
	llm.Detail = llmDetail

	// Step 2: the model's assessment of whether the brief can be targeted.
	intent := Step{Name: "intent_check", Status: "skipped"}
	if llm.Status == "ok" {
		intent.Status = "ok"
		if len(assess) > 0 {
			var a Assessment
			_ = json.Unmarshal(assess, &a)
			intent.Detail = a
		} else {
			intent.Detail = map[string]any{"outcome": "supported", "note": "recorded before outcome tracking"}
		}
	}
	executable := llm.Status == "ok" && (r.Outcome == nil || *r.Outcome == OutcomeSupported || *r.Outcome == OutcomeInvalid)

	// Step 3: deterministic backend validation of the proposed alternatives.
	val := Step{Name: "validation", Status: "skipped"}
	if executable {
		proposed := len(results)
		if raw != nil {
			var out struct {
				Alternatives []json.RawMessage `json:"alternatives"`
			}
			if json.Unmarshal([]byte(*raw), &out) == nil {
				proposed = len(out.Alternatives)
			}
		}
		val.Status = "ok"
		if len(results) == 0 {
			val.Status = "failed"
		}
		val.Detail = map[string]any{"proposed": proposed, "valid": len(results), "notes": nonNil(notes)}
	}

	// Step 4: read-only audience counts for the valid alternatives.
	res := Step{Name: "result", Status: "skipped"}
	if len(results) > 0 {
		res.Status = "ok"
		res.Detail = map[string]any{"alternatives": results}
	}
	r.Trace = []Step{llm, intent, val, res}
	return r, nil
}

// ListRuns returns recent runs, newest first, optionally filtered by status.
// Raw model output is only included by GetRun.
func (s *Service) ListRuns(ctx context.Context, status string, limit int) ([]Run, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT `+runColumns+` FROM segmentation_runs
		WHERE ($1 = '' OR status = $1) ORDER BY id DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ErrRunNotFound is returned by GetRun for unknown ids.
var ErrRunNotFound = errors.New("run not found")

// GetRun returns one run including the raw model output.
func (s *Service) GetRun(ctx context.Context, id int64) (Run, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	r, err := scanRun(s.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM segmentation_runs WHERE id = $1`, id), true)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrRunNotFound
	}
	return r, err
}
