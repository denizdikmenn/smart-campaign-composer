package content

import (
	"context"
	"errors"
	"math"
	"slices"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/smart-campaign-composer/backend/internal/llm"
	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

// historyWindow: cart additions older than this don't count as history.
const historyWindow = 180 * 24 * time.Hour

// MaxIndividual caps individual personalization (a demo, not a production
// rollout): at most this many customers get a message written for them,
// samples included. Everyone else in the audience gets the base message.
const MaxIndividual = 10

// PersonalMessage is one customer's individually generated message (or why
// they get the Standard template instead).
type PersonalMessage struct {
	CustomerID   int64     `json:"customer_id"`
	FirstName    string    `json:"first_name"`
	Language     string    `json:"language"`
	Status       string    `json:"status"` // pending | generated | fallback | failed
	Reason       string    `json:"reason,omitempty"`
	Version      Version   `json:"content"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	UpdatedAt    time.Time `json:"updated_at"`
	baseHash     string
}

// IndividualSummary is the progress of individual generation.
type IndividualSummary struct {
	Total     int               `json:"total"`
	Pending   int               `json:"pending"`
	Generated int               `json:"generated"`
	Fallback  int               `json:"fallback"`
	Failed    int               `json:"failed"`
	Stale     int               `json:"stale"` // generated from an earlier base text
	Running   bool              `json:"running"`
	Applied   bool              `json:"applied"` // Apply was confirmed
	Limit     int               `json:"limit"`   // MaxIndividual
	Samples   []PersonalMessage `json:"samples"` // every picked customer (at most Limit)
}

// HasHistory reports whether a member has enough activity to personalize.
func HasHistory(m segmentation.Member, now time.Time) bool {
	return m.OrderCount > 0 || (m.LastCartAt != nil && now.Sub(*m.LastCartAt) < historyWindow)
}

// personalInput is the exact customer data sent to the model: no email,
// phone, last name or other contact details.
func personalInput(h segmentation.History, now time.Time) PersonalInput {
	daysAgo := func(t time.Time) int { return int(now.Sub(t).Hours() / 24) }
	in := PersonalInput{FirstName: h.FirstName, City: h.City, OrderCount: h.OrderCount,
		RecentOrders: []PersonalOrder{}, RecentCarts: []PersonalCart{}}
	if h.LastOrderAt != nil {
		d := daysAgo(*h.LastOrderAt)
		in.DaysSinceLastOrder = &d
	}
	for _, o := range h.RecentOrders {
		in.RecentOrders = append(in.RecentOrders, PersonalOrder{Category: o.Category, DaysAgo: daysAgo(o.At), Amount: math.Round(o.Amount)})
	}
	for _, c := range h.RecentCarts {
		in.RecentCarts = append(in.RecentCarts, PersonalCart{Category: c.Category, DaysAgo: daysAgo(c.At)})
	}
	return in
}

func (s *Service) individualSummary(ctx context.Context, c Campaign) (IndividualSummary, error) {
	sum := IndividualSummary{Running: s.running(jobKey("individual", c.ID)), Limit: MaxIndividual, Samples: []PersonalMessage{}}
	rows, err := s.pool.Query(ctx, `SELECT status, count(*) FROM personalized_messages WHERE campaign_id = $1 GROUP BY status`, c.ID)
	if err != nil {
		return sum, err
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			rows.Close()
			return sum, err
		}
		sum.Total += n
		switch st {
		case "pending":
			sum.Pending = n
		case "generated":
			sum.Generated = n
		case "fallback":
			sum.Fallback = n
		case "failed":
			sum.Failed = n
		}
	}
	rows.Close()
	sum.Applied = c.IndividualAppliedAt != nil
	base, err := s.baseHashOf(ctx, c)
	if err != nil {
		return sum, err
	}
	if base != "" {
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM personalized_messages WHERE campaign_id = $1 AND status = 'generated' AND base_hash <> $2`, c.ID, base).Scan(&sum.Stale)
	}
	sum.Samples, err = s.personalMessages(ctx, c.ID, `status IN ('generated', 'failed', 'fallback')`, MaxIndividual)
	return sum, err
}

// baseHashOf is the hash of the selected draft's base-language text ("" if none).
func (s *Service) baseHashOf(ctx context.Context, c Campaign) (string, error) {
	if c.SelectedVersion == nil {
		return "", nil
	}
	d, err := s.draft(ctx, c, *c.SelectedVersion)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return hashOf(baseText(c, d)), nil
}

func (s *Service) personalMessages(ctx context.Context, id int64, where string, limit int) ([]PersonalMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.customer_id, cu.first_name, p.language, p.status, p.reason, p.sms_body, p.email_subject, p.email_body,
		p.input_tokens, p.output_tokens, p.updated_at, p.base_hash
		FROM personalized_messages p JOIN customers cu ON cu.id = p.customer_id
		WHERE p.campaign_id = $1 AND `+where+` ORDER BY p.status = 'generated' DESC, p.updated_at DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PersonalMessage{}
	for rows.Next() {
		var m PersonalMessage
		if err := rows.Scan(&m.CustomerID, &m.FirstName, &m.Language, &m.Status, &m.Reason, &m.Version.SMSBody, &m.Version.EmailSubject,
			&m.Version.EmailBody, &m.InputTokens, &m.OutputTokens, &m.UpdatedAt, &m.baseHash); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Candidate is an audience member offered as a sample customer.
type Candidate struct {
	segmentation.Member
	HasHistory bool `json:"has_history"`
}

// Candidates lists reachable members to pick a sample from: the most active
// first, plus a few without history so the fallback can be seen too.
func (s *Service) Candidates(ctx context.Context, id int64, limit int) ([]Candidate, error) {
	c, err := s.loadCampaign(ctx, s.pool, id, false)
	if err != nil {
		return nil, err
	}
	members, err := s.reachableMembers(ctx, c, nil)
	if err != nil {
		return nil, err
	}
	now := s.now()
	sort.SliceStable(members, func(i, j int) bool { return members[i].LastActiveAt.After(members[j].LastActiveAt) })
	out := []Candidate{}
	without := 0
	for _, m := range members {
		h := HasHistory(m, now)
		if !h && without >= 3 {
			continue
		}
		if !h {
			without++
		}
		out = append(out, Candidate{Member: m, HasHistory: h})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// SampleResult is one personalized sample plus what applying to everyone costs.
type SampleResult struct {
	Message   PersonalMessage `json:"message"`
	Input     *PersonalInput  `json:"input"` // exact data sent; nil for a fallback
	Remaining int             `json:"remaining"`
	Fallbacks int             `json:"fallbacks"` // reachable members without enough history
	Estimate  Estimate        `json:"estimate"`
}

// Sample generates one customer's personalized message (stored and reused
// by Apply) and estimates the cost of the remaining picks.
func (s *Service) Sample(ctx context.Context, id, customerID int64) (SampleResult, error) {
	if s.running(jobKey("individual", id)) {
		return SampleResult{}, ErrBusy
	}
	c, base, err := s.requireMode(ctx, id, ModeIndividual)
	if err != nil {
		return SampleResult{}, err
	}
	h, err := s.aud.CustomerHistory(ctx, c.Audience.rules(), customerID)
	if errors.Is(err, segmentation.ErrNotMember) {
		return SampleResult{}, invalid("this customer is not in the campaign audience")
	} else if err != nil {
		return SampleResult{}, err
	}
	if !h.Reachable() {
		return SampleResult{}, invalid("this customer has not consented to email or SMS")
	}
	picked, err := s.pickedCustomers(ctx, id)
	if err != nil {
		return SampleResult{}, err
	}
	if _, again := picked[customerID]; !again && len(picked) >= MaxIndividual {
		return SampleResult{}, invalid("individual messages are limited to %d customers in this demo; everyone else gets the base message", MaxIndividual)
	}
	var res SampleResult
	now := s.now()
	if HasHistory(h.Member, now) {
		in := personalInput(h, now)
		res.Input = &in
		res.Message, err = s.personalizeOne(ctx, c, base, h)
		if err != nil {
			return res, err
		}
	} else {
		res.Message = withBase(fallbackMessage(h.Member), base)
	}
	if err := s.storePersonal(ctx, c.ID, res.Message, hashOf(baseText(c, base)), false); err != nil {
		return res, err
	}
	res.Remaining, res.Fallbacks, err = s.individualWork(ctx, c)
	if err != nil {
		return res, err
	}
	res.Estimate = s.estimate(ctx, "individual", res.Remaining)
	return res, nil
}

// withBase fills a message that was not personalized with the base message
// in its language, so every picked customer shows the text they will get.
func withBase(m PersonalMessage, base Draft) PersonalMessage {
	if m.Status != "generated" && m.Version.empty() {
		m.Version, _ = pickVersion(base, m.Language)
		m.Version.BaseHash = ""
	}
	return m
}

// pickedCustomers are the customers already chosen for individual messages
// (samples and queued ones, any status), with their status.
func (s *Service) pickedCustomers(ctx context.Context, id int64) (map[int64]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT customer_id, status FROM personalized_messages WHERE campaign_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var cid int64
		var st string
		if err := rows.Scan(&cid, &st); err != nil {
			return nil, err
		}
		out[cid] = st
	}
	return out, rows.Err()
}

// nextPicks fills the remaining slots up to MaxIndividual with the most
// recently active members that have history (ties by id).
func nextPicks(members []segmentation.Member, picked map[int64]string, now time.Time) []segmentation.Member {
	ms := slices.Clone(members)
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].LastActiveAt.After(ms[j].LastActiveAt) })
	var out []segmentation.Member
	for _, m := range ms {
		if len(picked)+len(out) >= MaxIndividual {
			break
		}
		if _, ok := picked[m.ID]; !ok && HasHistory(m, now) {
			out = append(out, m)
		}
	}
	return out
}

func fallbackMessage(m segmentation.Member) PersonalMessage {
	return PersonalMessage{CustomerID: m.ID, FirstName: m.FirstName, Language: ContentLanguage(m.Language), Status: "fallback",
		Reason: "Not enough purchase or cart history; the Standard message is used."}
}

// individualWork counts the picked customers that still need a model call
// (pending or failed, plus the slots Apply will fill), and the members who get
// the base message instead (not picked, or picked without enough history).
func (s *Service) individualWork(ctx context.Context, c Campaign) (remaining, fallbacks int, err error) {
	picked, err := s.pickedCustomers(ctx, c.ID)
	if err != nil {
		return 0, 0, err
	}
	members, err := s.reachableMembers(ctx, c, nil)
	if err != nil {
		return 0, 0, err
	}
	for _, st := range picked {
		if st == "pending" || st == "failed" {
			remaining++
		}
	}
	next := len(nextPicks(members, picked, s.now()))
	remaining += next
	fallbacks = len(members) - remaining
	for _, st := range picked {
		if st == "generated" {
			fallbacks--
		}
	}
	return remaining, max(fallbacks, 0), nil
}

// IndividualEstimate is the cost of generating everyone still pending.
func (s *Service) IndividualEstimate(ctx context.Context, id int64) (SampleResult, error) {
	c, err := s.loadCampaign(ctx, s.pool, id, false)
	if err != nil {
		return SampleResult{}, err
	}
	var res SampleResult
	res.Remaining, res.Fallbacks, err = s.individualWork(ctx, c)
	res.Estimate = s.estimate(ctx, "individual", res.Remaining)
	return res, err
}

// personalizeOne generates and validates one customer's message. Model
// errors that are worth retrying are returned; anything else becomes a
// failed message (the customer then gets the Standard template).
func (s *Service) personalizeOne(ctx context.Context, c Campaign, base Draft, h segmentation.History) (PersonalMessage, error) {
	now := s.now()
	lang := ContentLanguage(h.Language)
	in := personalInput(h, now)
	msg := PersonalMessage{CustomerID: h.ID, FirstName: h.FirstName, Language: lang}
	bt := baseText(c, base)
	start := time.Now()
	tr := run{campaignID: c.ID, kind: "individual", customerID: h.ID, source: s.gen.Source(), model: s.gen.Model(),
		input: map[string]any{"language": lang, "customer": in, "base_version": base.Version}}
	res, err := s.gen.Personalize(ctx, PersonalizeRequest{Brief: c.Brief, Base: bt, BaseLanguage: c.BaseLanguage, Language: lang, Customer: in})
	tr.finish(start, res.Usage, err)
	s.record(ctx, tr)
	msg.InputTokens, msg.OutputTokens = res.InputTokens, res.OutputTokens
	if err != nil {
		if errors.Is(err, llm.ErrRetry) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return msg, err
		}
		msg.Status, msg.Reason = "failed", userError(err)
		return msg, nil
	}
	// Facts may come from the brief or the base message (in any language).
	facts := c.facts() + "\n" + bt.SMSBody + "\n" + bt.EmailSubject + "\n" + bt.EmailBody
	for _, t := range base.Translations {
		facts += "\n" + t.texts()
	}
	content := withVersion(Content{SMSEnabled: base.SMSEnabled, EmailEnabled: base.EmailEnabled, Settings: base.Settings}, res.Version)
	for _, i := range Validate(content, facts, utf8.RuneCountInString(h.FirstName)) {
		if i.Blocking || i.Code == "unverified_fact" {
			msg.Status, msg.Reason = "failed", "Rejected: "+i.Message
			return msg, nil
		}
	}
	msg.Status, msg.Version = "generated", res.Version
	return msg, nil
}

// storePersonal upserts a message. With onlyPending, rows that are no longer
// pending (e.g. stopped and reset) are not overwritten.
func (s *Service) storePersonal(ctx context.Context, id int64, m PersonalMessage, baseHash string, onlyPending bool) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), queryTimeout)
	defer cancel()
	cond := ""
	if onlyPending {
		cond = " WHERE personalized_messages.status = 'pending'"
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO personalized_messages
		(campaign_id, customer_id, language, status, sms_body, email_subject, email_body, reason, base_hash, input_tokens, output_tokens)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (campaign_id, customer_id) DO UPDATE SET language = EXCLUDED.language, status = EXCLUDED.status,
		sms_body = EXCLUDED.sms_body, email_subject = EXCLUDED.email_subject, email_body = EXCLUDED.email_body,
		reason = EXCLUDED.reason, base_hash = EXCLUDED.base_hash,
		input_tokens = personalized_messages.input_tokens + EXCLUDED.input_tokens,
		output_tokens = personalized_messages.output_tokens + EXCLUDED.output_tokens, updated_at = now()`+cond,
		id, m.CustomerID, m.Language, m.Status, m.Version.SMSBody, m.Version.EmailSubject, m.Version.EmailBody, m.Reason, baseHash,
		m.InputTokens, m.OutputTokens)
	return err
}

// ApplyIndividual fills the remaining slots up to MaxIndividual (most
// active customers with history), retries failed picks, and starts
// background generation. Nobody else gets a model call: they get the base
// message. Calling it again resumes after a stop or restart.
func (s *Service) ApplyIndividual(ctx context.Context, id int64) error {
	c, base, err := s.requireMode(ctx, id, ModeIndividual)
	if err != nil {
		return err
	}
	if s.running(jobKey("individual", id)) {
		return ErrBusy
	}
	picked, err := s.pickedCustomers(ctx, id)
	if err != nil {
		return err
	}
	samples := 0
	for _, st := range picked {
		if st == "generated" {
			samples++
		}
	}
	if samples == 0 {
		return invalid("generate and review a sample first")
	}
	members, err := s.reachableMembers(ctx, c, nil)
	if err != nil {
		return err
	}
	var ids []int64
	var langs []string
	for _, m := range nextPicks(members, picked, s.now()) {
		ids, langs = append(ids, m.ID), append(langs, ContentLanguage(m.Language))
	}
	err = s.mutate(ctx, id, func(tx pgx.Tx, _ Campaign) error {
		// Failed picks are retried on resume; generated ones are kept.
		if _, err := tx.Exec(ctx, `UPDATE personalized_messages SET status = 'pending', reason = '', updated_at = now()
			WHERE campaign_id = $1 AND status = 'failed'`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO personalized_messages (campaign_id, customer_id, language, status)
			SELECT $1, u.id, u.lang, 'pending' FROM unnest($2::bigint[], $3::text[]) AS u(id, lang)
			ON CONFLICT (campaign_id, customer_id) DO NOTHING`, id, ids, langs); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE campaigns SET individual_applied_at = COALESCE(individual_applied_at, now()) WHERE id = $1`, id)
		return err
	})
	if err != nil {
		return err
	}
	byID := map[int64]segmentation.Member{}
	for _, m := range members {
		byID[m.ID] = m
	}
	if !s.start(jobKey("individual", id), func(ctx context.Context) { s.runIndividual(ctx, c, base, byID) }) {
		return ErrBusy
	}
	return nil
}

// StopIndividual stops background generation; pending rows stay pending.
func (s *Service) StopIndividual(id int64) { s.stop(jobKey("individual", id)) }

// runIndividual generates pending messages with at most individualWorkers
// concurrent model calls, retrying a busy/unavailable API once.
func (s *Service) runIndividual(ctx context.Context, c Campaign, base Draft, members map[int64]segmentation.Member) {
	rows, err := s.pool.Query(ctx, `SELECT customer_id FROM personalized_messages WHERE campaign_id = $1 AND status = 'pending' ORDER BY customer_id`, c.ID)
	if err != nil {
		s.log.Error("individual generation: list pending", "campaign", c.ID, "error", err)
		return
	}
	var pending []int64
	for rows.Next() {
		var cid int64
		_ = rows.Scan(&cid)
		pending = append(pending, cid)
	}
	rows.Close()
	hash := hashOf(baseText(c, base))

	work := make(chan int64)
	var wg sync.WaitGroup
	for range s.individualWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for cid := range work {
				s.personalizePending(ctx, c, base, members, cid, hash)
			}
		}()
	}
	for _, cid := range pending {
		select {
		case work <- cid:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(work)
	wg.Wait()
}

func (s *Service) personalizePending(ctx context.Context, c Campaign, base Draft, members map[int64]segmentation.Member, cid int64, hash string) {
	m, ok := members[cid]
	if !ok { // left the audience since Apply: use the template
		f := fallbackMessage(segmentation.Member{ID: cid})
		f.Reason = "No longer in the audience."
		_ = s.storePersonal(ctx, c.ID, withBase(f, base), hash, true)
		return
	}
	h, err := s.aud.RecentActivity(ctx, m)
	if err != nil {
		return // stays pending; a resume retries it
	}
	var msg PersonalMessage
	for attempt := 0; attempt < 2; attempt++ {
		msg, err = s.personalizeOne(ctx, c, base, h)
		if err == nil || ctx.Err() != nil || !errors.Is(err, llm.ErrRetry) {
			break
		}
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
		}
	}
	switch {
	case ctx.Err() != nil:
		return // stopped: stays pending
	case err != nil:
		msg.Status, msg.Reason = "failed", userError(err)
	}
	if msg.Status != "generated" { // the customer gets the base message: show it
		msg.Version = Version{}
		msg = withBase(msg, base)
	}
	if err := s.storePersonal(ctx, c.ID, msg, hash, true); err != nil {
		s.log.Error("individual generation: store", "campaign", c.ID, "customer", cid, "error", err)
	}
}

// personalByCustomer loads generated individual messages for resolution.
func (s *Service) personalByCustomer(ctx context.Context, id int64) (map[int64]PersonalMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT customer_id, language, status, sms_body, email_subject, email_body
		FROM personalized_messages WHERE campaign_id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]PersonalMessage{}
	for rows.Next() {
		var m PersonalMessage
		if err := rows.Scan(&m.CustomerID, &m.Language, &m.Status, &m.Version.SMSBody, &m.Version.EmailSubject, &m.Version.EmailBody); err != nil {
			return nil, err
		}
		out[m.CustomerID] = m
	}
	return out, rows.Err()
}
