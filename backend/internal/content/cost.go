package content

import (
	"context"

	"github.com/smart-campaign-composer/backend/internal/llm"
)

// Cost is the AI generation cost of a campaign so far, from content_runs.
// It is unrelated to SMS/email delivery cost.
type Cost struct {
	Calls        int                 `json:"calls"`
	InputTokens  int                 `json:"input_tokens"`
	OutputTokens int                 `json:"output_tokens"`
	USD          *float64            `json:"usd"` // nil when a model has no known price (e.g. the mock)
	ByKind       map[string]KindCost `json:"by_kind"`
}

type KindCost struct {
	Calls        int      `json:"calls"`
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
	USD          *float64 `json:"usd"`
}

func addUSD(dst **float64, v float64) {
	if *dst == nil {
		*dst = new(float64)
	}
	**dst += v
}

func (s *Service) cost(ctx context.Context, id int64) (Cost, error) {
	c := Cost{ByKind: map[string]KindCost{}}
	rows, err := s.pool.Query(ctx, `SELECT kind, COALESCE(model, ''), count(*), COALESCE(sum(input_tokens), 0), COALESCE(sum(output_tokens), 0)
		FROM content_runs WHERE campaign_id = $1 GROUP BY kind, model`, id)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	unpriced := false
	for rows.Next() {
		var kind, model string
		var calls, in, out int
		if err := rows.Scan(&kind, &model, &calls, &in, &out); err != nil {
			return c, err
		}
		k := c.ByKind[kind]
		k.Calls, k.InputTokens, k.OutputTokens = k.Calls+calls, k.InputTokens+in, k.OutputTokens+out
		c.Calls, c.InputTokens, c.OutputTokens = c.Calls+calls, c.InputTokens+in, c.OutputTokens+out
		if p, ok := llm.PriceOf(model); ok {
			addUSD(&k.USD, p.USD(in, out))
			addUSD(&c.USD, p.USD(in, out))
		} else if in+out > 0 {
			unpriced = true
		}
		c.ByKind[kind] = k
	}
	if unpriced {
		c.USD = nil // a partial sum would understate the cost
	}
	return c, rows.Err()
}

// Estimate is the expected cost of a number of model calls of one kind.
type Estimate struct {
	Calls        int      `json:"calls"`
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens"`
	USD          *float64 `json:"usd"`
	Model        string   `json:"model"`
	Basis        string   `json:"basis"` // "observed" (average of earlier calls) or "default"
}

// Typical tokens per call when nothing has been observed yet.
var defaultTokens = map[string][2]int{
	"draft": {1500, 800}, "localize": {1200, 700}, "groups": {2500, 900}, "group_content": {1600, 900}, "individual": {1300, 450},
}

// estimate projects calls of kind with the current model, using the average
// of earlier successful calls of that kind and model when available.
func (s *Service) estimate(ctx context.Context, kind string, calls int) Estimate {
	e := Estimate{Calls: calls, Model: s.gen.Model(), Basis: "default"}
	in, out := defaultTokens[kind][0], defaultTokens[kind][1]
	var n, avgIn, avgOut int
	err := s.pool.QueryRow(ctx, `SELECT count(*), COALESCE(avg(input_tokens), 0)::int, COALESCE(avg(output_tokens), 0)::int
		FROM content_runs WHERE kind = $1 AND model = $2 AND status = 'ok' AND input_tokens > 0`, kind, e.Model).Scan(&n, &avgIn, &avgOut)
	if err == nil && n > 0 {
		in, out, e.Basis = avgIn, avgOut, "observed"
	}
	e.InputTokens, e.OutputTokens = in*calls, out*calls
	if p, ok := llm.PriceOf(e.Model); ok {
		v := p.USD(e.InputTokens, e.OutputTokens)
		e.USD = &v
	}
	return e
}
