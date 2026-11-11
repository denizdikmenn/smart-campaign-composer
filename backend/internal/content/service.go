package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

const (
	queryTimeout   = 5 * time.Second
	minBriefLen    = 10
	maxBriefLen    = 2000
	maxFeedbackLen = 1000
	maxVariants    = 3
)

// Personalization modes. Standard is the default; groups and individual are
// alternatives to an A/B experiment, not combinable with it.
const (
	ModeStandard   = "standard"
	ModeGroups     = "groups"
	ModeIndividual = "individual"
)

var (
	ErrNotFound = errors.New("not found")
	ErrApproved = errors.New("the campaign is already approved and can no longer be changed")
	ErrBusy     = errors.New("this campaign is busy with another generation; wait for it to finish")
	ErrInvalid  = errors.New("invalid input")
)

// ValidationError is returned by Approve when the campaign fails the
// backend checks. Issues are marketer-facing.
type ValidationError struct{ Issues []Issue }

func (e *ValidationError) Error() string { return "the campaign cannot be approved yet" }

// Audiences is what the content service needs from audience segmentation;
// implemented by *segmentation.Service.
type Audiences interface {
	CheckAudience(ctx context.Context, a segmentation.Alternative) (segmentation.AudienceCheck, error)
	ValidateRules(ctx context.Context, a segmentation.Alternative) (segmentation.Alternative, error)
	Members(ctx context.Context, audience segmentation.Alternative, groups []segmentation.Alternative, fn func(segmentation.Member) error) error
	CustomerHistory(ctx context.Context, audience segmentation.Alternative, customerID int64) (segmentation.History, error)
	RecentActivity(ctx context.Context, m segmentation.Member) (segmentation.History, error)
	Catalog(ctx context.Context) (segmentation.Catalog, error)
}

type Campaign struct {
	ID              int64           `json:"id"`
	CreatedAt       time.Time       `json:"created_at"`
	Brief           string          `json:"brief"`
	Audience        Audience        `json:"audience"`
	Status          string          `json:"status"` // draft | approved
	Personalization string          `json:"personalization"`
	SelectedVersion *int            `json:"selected_version"`
	Variants        []Variant       `json:"variants"`
	Languages       []string        `json:"languages"` // content languages, base first
	BaseLanguage    string          `json:"base_language"`
	GroupsNote      json.RawMessage `json:"groups_note,omitempty"`
	// IndividualAppliedAt is when "Apply to all" was confirmed.
	IndividualAppliedAt *time.Time      `json:"individual_applied_at"`
	ApprovedVersion     *int            `json:"approved_version"`
	ApprovedAt          *time.Time      `json:"approved_at"`
	Snapshot            json.RawMessage `json:"approved_snapshot,omitempty"`
	// feedback is every marketer feedback given on this campaign's drafts:
	// facts stated there (codes, amounts, dates) are as legitimate as the brief's.
	feedback string
}

// facts is the text that offers, amounts and codes in messages are checked against.
func (c Campaign) facts() string {
	if c.feedback == "" {
		return c.Brief
	}
	return c.Brief + "\n" + c.feedback
}

// Variant is an active A/B variant: a draft explicitly promoted by the marketer.
type Variant struct {
	Label        string `json:"label"` // A, B, C
	Position     int    `json:"position"`
	DraftVersion int    `json:"draft_version"`
}

type Draft struct {
	Version       int                `json:"version"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	Source        string             `json:"source"`
	Feedback      string             `json:"feedback"`
	Origin        string             `json:"origin"` // initial | regenerate | candidate | manual | duplicate
	ParentVersion *int               `json:"parent_version"`
	Language      string             `json:"language"`
	Translations  map[string]Version `json:"translations"`
	Content
	Localization []LangStatus `json:"localization"`
	Issues       []Issue      `json:"issues"`
	// Notice is a non-fatal note from the request that produced the draft
	// (e.g. translations could not be generated).
	Notice string `json:"notice,omitempty"`
}

// View is a campaign with everything the composer shows.
type View struct {
	Campaign   Campaign          `json:"campaign"`
	Drafts     []Draft           `json:"drafts"`
	Groups     []Group           `json:"groups"`
	Individual IndividualSummary `json:"individual"`
	Cost       Cost              `json:"cost"`
	Jobs       map[string]bool   `json:"jobs"` // running background jobs: groups, individual
	Options    Options           `json:"options"`
}

// Event is one streamed update. Type "field" carries the current value of a
// reply field; "localizing" announces the translation step.
type Event struct {
	Type      string   `json:"-"`
	Field     string   `json:"field,omitempty"`
	Value     string   `json:"value,omitempty"`
	Languages []string `json:"languages,omitempty"`
}

// streamedFields are reported to the client, in schema order.
var streamedFields = []string{"tone", "occasion", "length", "cta", "sms_body", "email_subject", "email_body"}

type Service struct {
	pool *pgxpool.Pool
	gen  Generator
	aud  Audiences
	log  *slog.Logger
	now  func() time.Time

	life context.Context // lifetime of background jobs (server lifetime)
	mu   sync.Mutex
	jobs map[string]*job // per-campaign locks and background jobs (single instance)

	individualWorkers int
}

func NewService(pool *pgxpool.Pool, gen Generator, aud Audiences, log *slog.Logger) *Service {
	return &Service{pool: pool, gen: gen, aud: aud, log: log, now: time.Now, life: context.Background(),
		jobs: map[string]*job{}, individualWorkers: 4}
}

// WithLifetime ties background jobs to ctx (cancelled on shutdown).
func (s *Service) WithLifetime(ctx context.Context) *Service {
	s.life = ctx
	return s
}

func invalid(msg string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(msg, args...))
}

// Create stores a new campaign for a brief and a selected audience. The
// rules are re-validated and recounted here; client-sent counts are ignored.
func (s *Service) Create(ctx context.Context, brief string, a Audience) (View, error) {
	brief = strings.TrimSpace(brief)
	if n := utf8.RuneCountInString(brief); n < minBriefLen || n > maxBriefLen {
		return View{}, invalid("brief must be between %d and %d characters", minBriefLen, maxBriefLen)
	}
	if strings.TrimSpace(a.Name) == "" {
		a.Name = "Selected audience"
	}
	a, err := s.recount(ctx, a)
	if err != nil {
		return View{}, err
	}
	if a.Counts.EmailEligible+a.Counts.SMSEligible == 0 {
		return View{}, invalid("no one in this audience can receive email or SMS")
	}
	aud, _ := json.Marshal(a)
	ctx2, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	var id int64
	if err := s.pool.QueryRow(ctx2, `INSERT INTO campaigns (brief, audience) VALUES ($1, $2) RETURNING id`, brief, aud).Scan(&id); err != nil {
		return View{}, err
	}
	return s.Get(ctx, id)
}

// recount re-validates the audience rules and refreshes counts and languages.
func (s *Service) recount(ctx context.Context, a Audience) (Audience, error) {
	check, err := s.aud.CheckAudience(ctx, a.rules())
	if errors.Is(err, segmentation.ErrInvalidRules) {
		return a, invalid("the audience rules are not valid")
	} else if err != nil {
		return a, err
	}
	a.ConditionText, a.Counts, a.MaxFirstNameLen = check.ConditionText, check.Counts, check.MaxFirstNameLen
	a.Languages = contentLanguages(check.Languages)
	return a, nil
}

const campaignCols = `id, created_at, brief, audience, status, personalization, selected_version, groups_note,
	individual_applied_at, approved_version, approved_at, approved_snapshot`

func scanCampaign(row pgx.Row) (Campaign, error) {
	var c Campaign
	var aud, snap, note []byte
	err := row.Scan(&c.ID, &c.CreatedAt, &c.Brief, &aud, &c.Status, &c.Personalization, &c.SelectedVersion, &note,
		&c.IndividualAppliedAt, &c.ApprovedVersion, &c.ApprovedAt, &snap)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(aud, &c.Audience); err != nil {
		return c, err
	}
	c.Snapshot, c.GroupsNote = snap, note
	c.Languages, c.BaseLanguage = c.Audience.LanguageList(), c.Audience.BaseLanguage()
	c.Variants = []Variant{}
	return c, nil
}

const draftCols = `version, created_at, updated_at, source, feedback, origin, parent_version, language, translations,
	sms_enabled, email_enabled, settings, sms_body, email_subject, email_body`

func scanDraft(row pgx.Row) (Draft, error) {
	var d Draft
	var settings, translations []byte
	err := row.Scan(&d.Version, &d.CreatedAt, &d.UpdatedAt, &d.Source, &d.Feedback, &d.Origin, &d.ParentVersion, &d.Language, &translations,
		&d.SMSEnabled, &d.EmailEnabled, &settings, &d.SMSBody, &d.EmailSubject, &d.EmailBody)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	d.Translations = map[string]Version{}
	if err := json.Unmarshal(translations, &d.Translations); err != nil {
		return d, err
	}
	return d, json.Unmarshal(settings, &d.Settings)
}

// annotate adds localization status and issues for the campaign's languages.
func (c Campaign) annotate(d *Draft) {
	d.Localization = d.localization(c.Languages)
	d.Issues = ValidateDraft(*d, c.facts(), c.Audience.MaxFirstNameLen, c.Languages)
	d.Issues = append(d.Issues, eligibilityIssues(*d, c.Audience)...)
}

func (s *Service) loadCampaign(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, id int64, forUpdate bool) (Campaign, error) {
	lock := ""
	if forUpdate {
		lock = " FOR UPDATE"
	}
	c, err := scanCampaign(q.QueryRow(ctx, `SELECT `+campaignCols+` FROM campaigns WHERE id = $1`+lock, id))
	if err != nil {
		return c, err
	}
	if err := q.QueryRow(ctx, `SELECT coalesce(string_agg(feedback, E'\n' ORDER BY version), '') FROM campaign_drafts WHERE campaign_id = $1 AND feedback <> ''`, id).Scan(&c.feedback); err != nil {
		return c, err
	}
	rows, err := q.Query(ctx, `SELECT position, draft_version FROM campaign_variants WHERE campaign_id = $1 ORDER BY position`, id)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Variant
		if err := rows.Scan(&v.Position, &v.DraftVersion); err != nil {
			return c, err
		}
		v.Label = string(rune('A' + v.Position - 1))
		c.Variants = append(c.Variants, v)
	}
	return c, rows.Err()
}

// mutate runs fn in a transaction holding the campaign row lock, after
// checking the campaign is still a draft. Every change goes through here, so
// an approved campaign can never be modified.
func (s *Service) mutate(ctx context.Context, id int64, fn func(tx pgx.Tx, c Campaign) error) error {
	ctx, cancel := context.WithTimeout(ctx, 2*queryTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	c, err := s.loadCampaign(ctx, tx, id, true)
	if err != nil {
		return err
	}
	if c.Status == "approved" {
		return ErrApproved
	}
	if err := fn(tx, c); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE campaigns SET updated_at = now() WHERE id = $1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) drafts(ctx context.Context, c Campaign) ([]Draft, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+draftCols+` FROM campaign_drafts WHERE campaign_id = $1 ORDER BY version`, c.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Draft{}
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		c.annotate(&d)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Service) draft(ctx context.Context, c Campaign, version int) (Draft, error) {
	d, err := scanDraft(s.pool.QueryRow(ctx, `SELECT `+draftCols+` FROM campaign_drafts WHERE campaign_id = $1 AND version = $2`, c.ID, version))
	if err != nil {
		return d, err
	}
	c.annotate(&d)
	return d, nil
}

// Get returns the campaign with drafts, groups, personalization progress and cost.
func (s *Service) Get(ctx context.Context, id int64) (View, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*queryTimeout)
	defer cancel()
	c, err := s.loadCampaign(ctx, s.pool, id, false)
	if err != nil {
		return View{}, err
	}
	v := View{Campaign: c, Options: SettingOptions, Jobs: map[string]bool{
		"groups": s.running(jobKey("groups", id)), "individual": s.running(jobKey("individual", id)),
	}}
	if v.Drafts, err = s.drafts(ctx, c); err != nil {
		return View{}, err
	}
	if v.Groups, err = s.groups(ctx, c, v.Drafts); err != nil {
		return View{}, err
	}
	if v.Individual, err = s.individualSummary(ctx, c); err != nil {
		return View{}, err
	}
	if v.Cost, err = s.cost(ctx, id); err != nil {
		return View{}, err
	}
	return v, nil
}

func findDraft(ds []Draft, version int) (Draft, bool) {
	for _, d := range ds {
		if d.Version == version {
			return d, true
		}
	}
	return Draft{}, false
}

// --- per-campaign locks and background jobs ----------------------------------------

type job struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func jobKey(kind string, id int64) string { return fmt.Sprintf("%s:%d", kind, id) }

// acquire takes a synchronous lock (no background work).
func (s *Service) acquire(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs[key] != nil {
		return false
	}
	s.jobs[key] = &job{cancel: func() {}, done: make(chan struct{})}
	return true
}

func (s *Service) release(key string) {
	s.mu.Lock()
	j := s.jobs[key]
	delete(s.jobs, key)
	s.mu.Unlock()
	if j != nil {
		close(j.done)
	}
}

// start runs fn in the background under key, detached from any request but
// cancelled on shutdown or by stop. It returns false if key is busy.
func (s *Service) start(key string, fn func(ctx context.Context)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.jobs[key] != nil {
		return false
	}
	ctx, cancel := context.WithCancel(s.life)
	j := &job{cancel: cancel, done: make(chan struct{})}
	s.jobs[key] = j
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("background job panicked", "job", key, "panic", r)
			}
			cancel()
			s.mu.Lock()
			delete(s.jobs, key)
			s.mu.Unlock()
			close(j.done)
		}()
		fn(ctx)
	}()
	return true
}

func (s *Service) stop(key string) {
	s.mu.Lock()
	j := s.jobs[key]
	s.mu.Unlock()
	if j != nil {
		j.cancel()
	}
}

func (s *Service) running(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[key] != nil
}

// Wait blocks until the background job of kind ("groups", "individual") for
// the campaign has finished. Used by tests and graceful shutdown.
func (s *Service) Wait(kind string, id int64) {
	s.mu.Lock()
	j := s.jobs[jobKey(kind, id)]
	s.mu.Unlock()
	if j != nil {
		<-j.done
	}
}
