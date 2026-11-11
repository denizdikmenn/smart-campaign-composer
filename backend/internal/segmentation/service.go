package segmentation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smart-campaign-composer/backend/internal/customers"
)

const queryTimeout = 5 * time.Second

var ErrInvalidRules = errors.New("invalid rules")
var ErrNoValidAlternatives = errors.New("none of the AI proposals could be validated")

type Counts struct {
	Total         int `json:"total"`
	EmailEligible int `json:"email_eligible"`
	SMSEligible   int `json:"sms_eligible"`
}

type Result struct {
	ID            string      `json:"id"`
	Primary       bool        `json:"primary"` // faithful reading of the brief
	Name          string      `json:"name"`
	Reasoning     string      `json:"reasoning"`
	Match         string      `json:"match"`
	Conditions    []Condition `json:"conditions"`
	ConditionText []string    `json:"conditions_text"`
	Counts        Counts      `json:"counts"`
	Empty         bool        `json:"empty"` // valid rules, but no customer matches today
}

// Response is the suggest API contract. Outcome decides what the UI shows:
//   - supported: Alternatives holds executable, counted audiences (possibly empty ones)
//   - needs_clarification: Message + Questions, no alternatives
//   - unsupported: Message + Unsupported (+ Suggestions), no alternatives
//   - invalid: the model claimed support but nothing passed validation; Warnings explain why
type Response struct {
	RunID          int64    `json:"run_id,omitempty"` // trace id in segmentation_runs
	Source         string   `json:"source"`           // "claude" or "mock"
	Model          string   `json:"model,omitempty"`
	Outcome        string   `json:"outcome"`
	Message        string   `json:"message"`
	Questions      []string `json:"questions"`
	Unsupported    []string `json:"unsupported_requirements"`
	Suggestions    []string `json:"suggestions"`
	TotalCustomers int      `json:"total_customers"`
	Alternatives   []Result `json:"alternatives"`
	Warnings       []string `json:"warnings"` // marketer-facing; technical detail stays in the trace
}

type Service struct {
	pool *pgxpool.Pool
	gen  Generator
	log  *slog.Logger
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool, gen Generator, log *slog.Logger) *Service {
	return &Service{pool: pool, gen: gen, log: log, now: time.Now}
}

// Suggest runs brief -> alternatives -> validation -> read-only counts and
// records a trace row for every outcome.
func (s *Service) Suggest(ctx context.Context, brief string) (Response, error) {
	start := time.Now()
	tr := run{brief: brief, source: s.gen.Source()}
	resp, err := s.suggest(ctx, brief, &tr)

	tr.model, tr.totalMs = resp.Model, int(time.Since(start).Milliseconds())
	tr.status = statusOf(err)
	if err != nil {
		tr.errMsg = err.Error()
	} else if st := outcomeStatus[resp.Outcome]; st != "" {
		tr.status = st
	}
	tr.outcome = resp.Outcome
	tr.assessment = Assessment{Outcome: resp.Outcome, Message: resp.Message, Questions: resp.Questions, Unsupported: resp.Unsupported, Suggestions: resp.Suggestions}
	tr.alternatives = resp.Alternatives
	resp.RunID = s.record(ctx, tr)
	return resp, err
}

func (s *Service) suggest(ctx context.Context, brief string, tr *run) (Response, error) {
	resp := Response{Source: s.gen.Source(), Alternatives: []Result{}, Warnings: []string{},
		Questions: []string{}, Unsupported: []string{}, Suggestions: []string{}}
	if c, ok := s.gen.(*Claude); ok {
		resp.Model = c.model
	}
	now := s.now()

	cat, total, err := s.catalog(ctx)
	if err != nil {
		return resp, err
	}
	resp.TotalCustomers = total

	t0 := time.Now()
	gen, err := s.gen.Generate(ctx, brief, cat, now)
	tr.llmMs = int(time.Since(t0).Milliseconds())
	tr.rawOutput, tr.inputTokens, tr.outputTokens = gen.Raw, gen.InputTokens, gen.OutputTokens
	if err != nil {
		return resp, err
	}
	resp.Outcome, resp.Message = gen.Outcome, gen.Message
	resp.Questions, resp.Unsupported, resp.Suggestions = nonNil(gen.Questions), nonNil(gen.Unsupported), nonNil(gen.Suggestions)
	if gen.Outcome != OutcomeSupported {
		// Never execute anything the model itself could not target accurately.
		if len(gen.Alternatives) > 0 {
			tr.notes = append(tr.notes, fmt.Sprintf("ignored %d alternative(s) returned with outcome %s", len(gen.Alternatives), gen.Outcome))
		}
		return resp, nil
	}

	type slot struct {
		res  Result
		ok   bool
		user string // marketer-facing reason when not ok
	}
	slots := make([]slot, len(gen.Alternatives))
	var wg sync.WaitGroup
	for i, raw := range gen.Alternatives {
		a, notes := Normalize(raw)
		for _, n := range notes {
			tr.notes = append(tr.notes, fmt.Sprintf("alternative %d: %s", i+1, n))
		}
		label := strings.TrimSpace(a.Name)
		if label == "" {
			label = fmt.Sprintf("Option %d", i+1)
		}
		if err := Validate(a, cat); err != nil {
			user := "it could not be checked"
			var p *Problem
			if errors.As(err, &p) {
				user = p.User
			}
			slots[i].user = fmt.Sprintf("%q was left out because %s.", label, user)
			tr.notes = append(tr.notes, fmt.Sprintf("alternative %d rejected: %s", i+1, err))
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			where, args := Compile(a, now)
			counts, err := s.count(ctx, where, args)
			if err != nil {
				slots[i].user = fmt.Sprintf("%q could not be counted. Please retry.", label)
				return
			}
			slots[i] = slot{ok: true, res: Result{
				ID: fmt.Sprintf("alt-%d", i+1), Primary: i == 0, Name: label, Reasoning: strings.TrimSpace(a.Reasoning), Match: a.Match,
				Conditions: a.Conditions, ConditionText: Describe(a), Counts: counts, Empty: counts.Total == 0,
			}}
		}()
	}
	wg.Wait()

	for _, sl := range slots {
		if sl.ok {
			resp.Alternatives = append(resp.Alternatives, sl.res)
		} else if sl.user != "" {
			resp.Warnings = append(resp.Warnings, sl.user)
		}
	}
	switch {
	case len(resp.Alternatives) == 0:
		resp.Outcome = OutcomeInvalid
		resp.Message = "We couldn't build a reliable audience from this brief. Try stating the targeting rules more concretely."
		return resp, nil
	case !slots[0].ok:
		// The faithful interpretation failed; the rest are variants, so none is
		// promoted to "best match".
		resp.Warnings = append(resp.Warnings, "The option that matched your brief exactly couldn't be built, so the remaining options differ from it. Review them carefully.")
	}
	resp.Warnings = append(resp.Warnings, consentNotices(resp.Alternatives)...)
	return resp, nil
}

// outcomeStatus maps a successful response's outcome to its trace status.
var outcomeStatus = map[string]string{
	OutcomeSupported: "ok", OutcomeClarification: "clarification", OutcomeUnsupported: "unsupported", OutcomeInvalid: "no_valid",
}

// consentNotices flags audiences that target people who opted out of a
// channel: they stay valid audiences, but are not reachable on that channel.
func consentNotices(alts []Result) []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range alts {
		for _, c := range a.Conditions {
			if c.Op != "is_false" || seen[c.Field] {
				continue
			}
			switch c.Field {
			case "sms_consent":
				out = append(out, "These customers opted out of SMS, so none of them can receive an SMS campaign.")
			case "email_consent":
				out = append(out, "These customers opted out of email, so none of them can receive an email campaign.")
			default:
				continue
			}
			seen[c.Field] = true
		}
	}
	return out
}

func (s *Service) catalog(ctx context.Context) (Catalog, int, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	var cat Catalog
	var total int
	err := s.pool.QueryRow(ctx, `SELECT count(*),
		COALESCE((SELECT array_agg(DISTINCT city ORDER BY city) FROM customers), '{}'),
		COALESCE((SELECT array_agg(DISTINCT language ORDER BY language) FROM customers), '{}'),
		COALESCE((SELECT array_agg(DISTINCT category ORDER BY category) FROM orders), '{}')
		FROM customers`).Scan(&total, &cat.Cities, &cat.Languages, &cat.Categories)
	return cat, total, err
}

// count runs the compiled predicate in a read-only transaction.
func (s *Service) count(ctx context.Context, where string, args []any) (Counts, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Counts{}, err
	}
	defer tx.Rollback(ctx)
	var c Counts
	err = tx.QueryRow(ctx, customers.StatsCTE+`SELECT count(*),
		count(*) FILTER (WHERE email_consent), count(*) FILTER (WHERE sms_consent)
		FROM s WHERE `+where, args...).Scan(&c.Total, &c.EmailEligible, &c.SMSEligible)
	return c, err
}

// PreviewCustomer is a small, display-only view of a matching customer.
type PreviewCustomer struct {
	FirstName    string  `json:"first_name"`
	LastName     string  `json:"last_name"`
	Email        string  `json:"email"`
	City         string  `json:"city"`
	Language     string  `json:"language"`
	EmailConsent bool    `json:"email_consent"`
	SMSConsent   bool    `json:"sms_consent"`
	Orders       int     `json:"orders"`
	TotalSpent   float64 `json:"total_spent"`
}

// Preview validates the rules again (never trust the client) and returns a
// few matching customers, most recently active first.
func (s *Service) Preview(ctx context.Context, a Alternative, limit int) ([]PreviewCustomer, error) {
	cat, _, err := s.catalog(ctx)
	if err != nil {
		return nil, err
	}
	a.Name, a.Reasoning = "preview", "Preview of matching customers."
	if err := Validate(a, cat); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidRules, err)
	}
	where, args := Compile(a, s.now())
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, customers.StatsCTE+`SELECT first_name, last_name, email, city, language, email_consent, sms_consent, order_count, total_spent
		FROM s WHERE `+where+` ORDER BY last_active_at DESC, id LIMIT `+fmt.Sprint(limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PreviewCustomer{}
	for rows.Next() {
		var c PreviewCustomer
		if err := rows.Scan(&c.FirstName, &c.LastName, &c.Email, &c.City, &c.Language, &c.EmailConsent, &c.SMSConsent, &c.Orders, &c.TotalSpent); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AudienceCheck is the server-side re-check of a stored audience: validated
// rules, current counts and the longest first name among reachable members
// (used to bound personalized message length).
type AudienceCheck struct {
	Counts          Counts   `json:"counts"`
	ConditionText   []string `json:"conditions_text"`
	MaxFirstNameLen int      `json:"max_first_name_len"`
	// Reachable (email or SMS consent) members per customer language code.
	Languages map[string]int `json:"languages"`
}

// CheckAudience validates rules against the current catalog (never trust the
// client or an earlier result) and recounts them in a read-only transaction.
func (s *Service) CheckAudience(ctx context.Context, a Alternative) (AudienceCheck, error) {
	cat, _, err := s.catalog(ctx)
	if err != nil {
		return AudienceCheck{}, err
	}
	// Only the rules are re-checked; name/reasoning are display metadata.
	a.Name, a.Reasoning = "stored audience", "Server-side re-check of stored rules."
	a, _ = Normalize(a)
	if err := Validate(a, cat); err != nil {
		return AudienceCheck{}, fmt.Errorf("%w: %v", ErrInvalidRules, err)
	}
	where, args := Compile(a, s.now())
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return AudienceCheck{}, err
	}
	defer tx.Rollback(ctx)
	out := AudienceCheck{ConditionText: Describe(a)}
	err = tx.QueryRow(ctx, customers.StatsCTE+`SELECT count(*),
		count(*) FILTER (WHERE email_consent), count(*) FILTER (WHERE sms_consent),
		COALESCE(max(char_length(first_name)) FILTER (WHERE email_consent OR sms_consent), 0)
		FROM s WHERE `+where, args...).Scan(&out.Counts.Total, &out.Counts.EmailEligible, &out.Counts.SMSEligible, &out.MaxFirstNameLen)
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, customers.StatsCTE+`SELECT language, count(*) FROM s
		WHERE (email_consent OR sms_consent) AND `+where+` GROUP BY language`, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	out.Languages = map[string]int{}
	for rows.Next() {
		var l string
		var n int
		if err := rows.Scan(&l, &n); err != nil {
			return out, err
		}
		out.Languages[l] = n
	}
	return out, rows.Err()
}
