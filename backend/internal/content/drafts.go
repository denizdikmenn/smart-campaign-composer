package content

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// GenerateInput selects what a generation does. FromVersion 0 writes Draft 1;
// otherwise that draft (with saved edits and settings) is the base. Intent is
// "regenerate" (revise the base) or "candidate" (a distinct A/B alternative).
type GenerateInput struct {
	FromVersion int    `json:"from_version"`
	Feedback    string `json:"feedback"`
	Intent      string `json:"intent"`
}

// Generate writes a new draft, streaming field values through emit, and then
// localizes it into the audience's other languages. The draft is stored only
// after the reply completed and parsed, so an interrupted stream never
// creates or replaces a draft. Earlier drafts are never modified.
func (s *Service) Generate(ctx context.Context, id int64, in GenerateInput, emit func(Event)) (Draft, error) {
	in.Feedback = strings.TrimSpace(in.Feedback)
	if utf8.RuneCountInString(in.Feedback) > maxFeedbackLen {
		return Draft{}, invalid("feedback must be at most %d characters", maxFeedbackLen)
	}
	if in.Intent == "" {
		in.Intent = "regenerate"
	}
	if in.Intent != "regenerate" && in.Intent != "candidate" {
		return Draft{}, invalid("unknown intent %q", in.Intent)
	}
	if !s.acquire(jobKey("draft", id)) {
		return Draft{}, ErrBusy
	}
	defer s.release(jobKey("draft", id))

	v, err := s.Get(ctx, id)
	if err != nil {
		return Draft{}, err
	}
	c := v.Campaign
	if c.Status == "approved" {
		return Draft{}, ErrApproved
	}
	req := Request{Brief: c.Brief, Audience: c.Audience, Feedback: in.Feedback, Language: c.BaseLanguage, Intent: in.Intent}
	channels := Content{SMSEnabled: true, EmailEnabled: true}
	origin := "initial"
	var parent *int
	switch {
	case in.FromVersion != 0:
		base, ok := findDraft(v.Drafts, in.FromVersion)
		if !ok {
			return Draft{}, ErrNotFound
		}
		bc := base.Content
		if base.Language != c.BaseLanguage { // e.g. a draft written before localization
			if t, ok := base.TextIn(c.BaseLanguage); ok {
				bc = withVersion(bc, t)
			}
		}
		req.Base, channels, origin, parent = &bc, base.Content, "regenerate", &in.FromVersion
		if in.Intent == "candidate" {
			origin = "candidate"
			for _, va := range c.Variants {
				if d, ok := findDraft(v.Drafts, va.DraftVersion); ok {
					req.Others = append(req.Others, d.Content)
				}
			}
			if len(req.Others) == 0 {
				req.Others = []Content{bc}
			}
		}
	case len(v.Drafts) > 0:
		return Draft{}, invalid("the first draft already exists; regenerate from a draft instead")
	case in.Intent == "candidate":
		return Draft{}, invalid("a candidate needs a reference draft")
	}

	start := time.Now()
	tr := run{campaignID: id, kind: "draft", fromVersion: in.FromVersion, source: s.gen.Source(), model: s.gen.Model(),
		input: map[string]any{"feedback": in.Feedback, "intent": in.Intent, "language": c.BaseLanguage, "base_settings": baseSettings(req.Base)}}

	var buf strings.Builder
	last := map[string]string{}
	onDelta := func(text string) {
		buf.WriteString(text)
		vals := partialStrings(buf.String())
		for _, f := range streamedFields {
			if val, ok := vals[f]; ok && val != last[f] {
				last[f] = val
				emit(Event{Type: "field", Field: f, Value: val})
			}
		}
	}
	res, err := s.gen.Generate(ctx, req, onDelta)
	tr.finish(start, res.Usage, err)
	var d Draft
	if err == nil {
		r := res.Reply
		content := Content{SMSEnabled: channels.SMSEnabled, EmailEnabled: channels.EmailEnabled, Settings: r.Settings,
			SMSBody: r.SMSBody, EmailSubject: r.EmailSubject, EmailBody: r.EmailBody}
		if req.Base != nil {
			content.Settings = req.Base.Settings // the marketer's choice wins over the model's echo
		}
		d, err = s.insertDraft(ctx, id, newDraft{Content: content, Feedback: in.Feedback, Origin: origin, Parent: parent, Language: c.BaseLanguage})
		if err == nil {
			tr.draftVersion = d.Version
			d.Notice = r.Notes
		}
	}
	if err != nil {
		tr.fail(err)
	}
	s.record(ctx, tr)
	if err != nil {
		return d, err
	}

	// Localize into the audience's other languages (one call for all).
	if targets := missingLanguages(d, c.Languages, false); len(targets) > 0 {
		emit(Event{Type: "localizing", Languages: targets})
		if err := s.translateDraft(ctx, c, &d, targets); err != nil {
			s.log.Warn("localization failed", "campaign", id, "error", err)
			d.Notice = strings.TrimSpace(d.Notice + " The draft was saved, but translations could not be generated. Use “Update translations” to retry.")
		}
		if fresh, err := s.draft(ctx, c, d.Version); err == nil {
			fresh.Notice = d.Notice
			d = fresh
		}
	}
	if in.Feedback != "" {
		c.feedback = strings.TrimSpace(c.feedback + "\n" + in.Feedback)
	}
	c.annotate(&d)
	return d, nil
}

func baseSettings(c *Content) any {
	if c == nil {
		return nil
	}
	return c.Settings
}

// missingLanguages lists required languages without a current translation
// (and, with stale, those translated from an earlier base text).
func missingLanguages(d Draft, langs []string, stale bool) []string {
	var out []string
	for _, ls := range d.localization(langs) {
		if ls.Status == "missing" || (stale && ls.Status == "stale") {
			out = append(out, ls.Language)
		}
	}
	return out
}

// translateDraft translates d's base text into targets and stores them.
func (s *Service) translateDraft(ctx context.Context, c Campaign, d *Draft, targets []string) error {
	start := time.Now()
	tr := run{campaignID: c.ID, kind: "localize", fromVersion: d.Version, draftVersion: d.Version, source: s.gen.Source(), model: s.gen.Model(),
		input: map[string]any{"source_language": d.Language, "targets": targets}}
	res, err := s.gen.Translate(ctx, TranslateRequest{Brief: c.Brief, Source: d.Content, SourceLanguage: d.Language, Targets: targets, MaxNameLen: c.Audience.MaxFirstNameLen})
	tr.finish(start, res.Usage, err)
	if err == nil {
		hash := hashOf(d.Content)
		err = s.mutate(ctx, c.ID, func(tx pgx.Tx, _ Campaign) error {
			cur, err := scanDraft(tx.QueryRow(ctx, `SELECT `+draftCols+` FROM campaign_drafts WHERE campaign_id = $1 AND version = $2 FOR UPDATE`, c.ID, d.Version))
			if err != nil {
				return err
			}
			if hashOf(cur.Content) != hash {
				return invalid("the draft was edited while translating; update translations again")
			}
			for l, v := range res.Versions {
				v.BaseHash = hash
				cur.Translations[l] = v
			}
			tj, _ := json.Marshal(cur.Translations)
			if _, err := tx.Exec(ctx, `UPDATE campaign_drafts SET translations = $3, updated_at = now() WHERE campaign_id = $1 AND version = $2`, c.ID, d.Version, tj); err != nil {
				return err
			}
			d.Translations = cur.Translations
			return nil
		})
	}
	if err != nil {
		tr.fail(err)
	}
	s.record(ctx, tr)
	return err
}

// Localize (re)generates translations that are missing or stale.
func (s *Service) Localize(ctx context.Context, id int64, version int) (Draft, error) {
	if !s.acquire(jobKey("draft", id)) {
		return Draft{}, ErrBusy
	}
	defer s.release(jobKey("draft", id))
	c, err := s.loadCampaign(ctx, s.pool, id, false)
	if err != nil {
		return Draft{}, err
	}
	if c.Status == "approved" {
		return Draft{}, ErrApproved
	}
	d, err := s.draft(ctx, c, version)
	if err != nil {
		return d, err
	}
	targets := missingLanguages(d, c.Languages, true)
	if len(targets) == 0 {
		return d, nil
	}
	if strings.TrimSpace(d.SMSBody+d.EmailSubject+d.EmailBody) == "" {
		return d, invalid("write the %s text first", LanguageName(d.Language))
	}
	if err := s.translateDraft(ctx, c, &d, targets); err != nil {
		return d, err
	}
	c.annotate(&d)
	return d, nil
}

type newDraft struct {
	Content
	Feedback     string
	Origin       string
	Parent       *int
	Language     string
	Translations map[string]Version
}

// insertDraft appends the next version, unless the campaign got approved.
func (s *Service) insertDraft(ctx context.Context, id int64, n newDraft) (Draft, error) {
	var d Draft
	err := s.mutate(ctx, id, func(tx pgx.Tx, c Campaign) error {
		if n.Translations == nil {
			n.Translations = map[string]Version{}
		}
		settings, _ := json.Marshal(n.Settings)
		translations, _ := json.Marshal(n.Translations)
		var err error
		d, err = scanDraft(tx.QueryRow(ctx, `INSERT INTO campaign_drafts
			(campaign_id, version, source, feedback, origin, parent_version, language, translations,
			 sms_enabled, email_enabled, settings, sms_body, email_subject, email_body)
			VALUES ($1, (SELECT COALESCE(max(version), 0) + 1 FROM campaign_drafts WHERE campaign_id = $1),
			        $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			RETURNING `+draftCols, id, s.draftSource(n.Origin), n.Feedback, n.Origin, n.Parent, n.Language, translations,
			n.SMSEnabled, n.EmailEnabled, settings, n.SMSBody, n.EmailSubject, n.EmailBody))
		if err != nil {
			return err
		}
		c.annotate(&d)
		return nil
	})
	return d, err
}

func (s *Service) draftSource(origin string) string {
	if origin == "manual" || origin == "duplicate" {
		return "manual"
	}
	return s.gen.Source()
}

// CreateDraft adds a draft without AI: a copy of fromVersion (duplicate, with
// its translations), or a blank draft (manual) using the settings and
// channels of the selected or latest draft.
func (s *Service) CreateDraft(ctx context.Context, id int64, fromVersion int) (Draft, error) {
	c, err := s.loadCampaign(ctx, s.pool, id, false)
	if err != nil {
		return Draft{}, err
	}
	ds, err := s.drafts(ctx, c)
	if err != nil {
		return Draft{}, err
	}
	if fromVersion > 0 {
		src, ok := findDraft(ds, fromVersion)
		if !ok {
			return Draft{}, ErrNotFound
		}
		return s.insertDraft(ctx, id, newDraft{Content: src.Content, Origin: "duplicate", Parent: &fromVersion,
			Language: src.Language, Translations: src.Translations})
	}
	n := newDraft{Content: Content{SMSEnabled: true, EmailEnabled: true, Settings: Settings{Tone: "friendly", Occasion: "promotion", Length: "balanced", CTA: "shop_now"}},
		Origin: "manual", Language: c.BaseLanguage}
	ref := 0
	if c.SelectedVersion != nil {
		ref = *c.SelectedVersion
	} else if len(ds) > 0 {
		ref = ds[len(ds)-1].Version
	}
	if src, ok := findDraft(ds, ref); ok {
		n.Settings, n.SMSEnabled, n.EmailEnabled = src.Settings, src.SMSEnabled, src.EmailEnabled
	}
	return s.insertDraft(ctx, id, n)
}

// DraftEdit is the marketer's edit of a draft: the base text, settings,
// channels and (optionally) translations.
type DraftEdit struct {
	Content
	Translations map[string]Version `json:"translations"`
}

// SaveDraft stores the marketer's edits. An edited translation counts as
// reviewed against the current base text; untouched ones keep their state.
func (s *Service) SaveDraft(ctx context.Context, id int64, version int, e DraftEdit) (Draft, error) {
	if !e.Settings.Valid() {
		return Draft{}, invalid("unknown tone, occasion, length or call to action")
	}
	texts := []string{e.SMSBody, e.EmailSubject, e.EmailBody}
	for _, v := range e.Translations {
		texts = append(texts, v.SMSBody, v.EmailSubject, v.EmailBody)
	}
	for _, t := range texts {
		if len(t) > maxFieldLen {
			return Draft{}, invalid("message text is too long")
		}
	}
	var d Draft
	err := s.mutate(ctx, id, func(tx pgx.Tx, c Campaign) error {
		cur, err := scanDraft(tx.QueryRow(ctx, `SELECT `+draftCols+` FROM campaign_drafts WHERE campaign_id = $1 AND version = $2 FOR UPDATE`, id, version))
		if err != nil {
			return err
		}
		hash := hashOf(e.Content)
		for l, v := range e.Translations {
			if l == cur.Language || !slices.Contains(SupportedLanguages, l) {
				continue
			}
			old, had := cur.Translations[l]
			if v.empty() {
				delete(cur.Translations, l)
				continue
			}
			v.BaseHash = old.BaseHash
			if !had || old.texts() != v.texts() {
				v.BaseHash = hash // written or edited by the marketer against this base
			}
			cur.Translations[l] = v
		}
		settings, _ := json.Marshal(e.Settings)
		translations, _ := json.Marshal(cur.Translations)
		d, err = scanDraft(tx.QueryRow(ctx, `UPDATE campaign_drafts SET sms_enabled = $3, email_enabled = $4, settings = $5,
			sms_body = $6, email_subject = $7, email_body = $8, translations = $9, updated_at = now()
			WHERE campaign_id = $1 AND version = $2 RETURNING `+draftCols,
			id, version, e.SMSEnabled, e.EmailEnabled, settings, e.SMSBody, e.EmailSubject, e.EmailBody, translations))
		if err != nil {
			return err
		}
		c.annotate(&d)
		return nil
	})
	return d, err
}
