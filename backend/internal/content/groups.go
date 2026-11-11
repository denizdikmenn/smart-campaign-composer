package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

const (
	maxGroups        = 10 // including the remainder group
	minGroupAudience = 20 // below this, grouping is not meaningful
	minGroupSize     = 10 // a rule group smaller than this is folded into later groups
	groupWorkers     = 3
)

// Group is a behavioral group of the campaign audience with its own
// messages per language. Position 0 is the remainder ("everyone else").
type Group struct {
	Position      int                      `json:"position"`
	Name          string                   `json:"name"`
	Description   string                   `json:"description"`
	Match         string                   `json:"match"`
	Conditions    []segmentation.Condition `json:"conditions"`
	ConditionText []string                 `json:"conditions_text"`
	Size          int                      `json:"size"`      // reachable members
	Languages     map[string]int           `json:"languages"` // reachable members per content language
	Content       map[string]Version       `json:"content"`
	Status        string                   `json:"status"` // proposed | generating | generated | failed
	Error         string                   `json:"error,omitempty"`
	Stale         bool                     `json:"stale"` // written from an earlier base draft text
	Issues        []Issue                  `json:"issues"`
	remainder     bool
}

func (g Group) rules() segmentation.Alternative {
	return segmentation.Alternative{Name: g.Name, Reasoning: g.Description, Match: g.Match, Conditions: g.Conditions}
}

// languageList is the group's languages, most common first.
func (g Group) languageList() []string { return Audience{Languages: g.Languages}.LanguageList() }

// GroupsNote is the outcome of the last grouping proposal.
type GroupsNote struct {
	Outcome  string   `json:"outcome"` // groups | not_meaningful
	Message  string   `json:"message"`
	Warnings []string `json:"warnings"`
}

func groupRules(gs []Group) []segmentation.Alternative {
	var out []segmentation.Alternative
	for _, g := range gs {
		if !g.remainder {
			out = append(out, g.rules())
		}
	}
	return out
}

// baseText is the base draft in the campaign's base language.
func baseText(c Campaign, d Draft) Content {
	if v, ok := d.TextIn(c.BaseLanguage); ok {
		return withVersion(d.Content, v)
	}
	return d.Content
}

// selectedDraft is the base draft for groups and individual personalization.
func selectedDraft(c Campaign, ds []Draft) (Draft, bool) {
	if c.SelectedVersion == nil {
		return Draft{}, false
	}
	return findDraft(ds, *c.SelectedVersion)
}

func (s *Service) groups(ctx context.Context, c Campaign, ds []Draft) ([]Group, error) {
	rows, err := s.pool.Query(ctx, `SELECT position, name, description, match, conditions, size, languages, content, status, COALESCE(error, '')
		FROM campaign_groups WHERE campaign_id = $1 ORDER BY position = 0, position`, c.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	base, hasBase := selectedDraft(c, ds)
	baseHash := ""
	if hasBase {
		baseHash = hashOf(baseText(c, base))
	}
	out := []Group{}
	for rows.Next() {
		var g Group
		var conds, langs, content []byte
		if err := rows.Scan(&g.Position, &g.Name, &g.Description, &g.Match, &conds, &g.Size, &langs, &content, &g.Status, &g.Error); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(conds, &g.Conditions)
		_ = json.Unmarshal(langs, &g.Languages)
		_ = json.Unmarshal(content, &g.Content)
		if g.Content == nil {
			g.Content = map[string]Version{}
		}
		g.remainder = g.Position == 0
		if g.Status == "generating" && !s.running(jobKey("groups", c.ID)) {
			g.Status, g.Error = "failed", "Generation was interrupted. Generate again."
		}
		g.ConditionText = []string{"Everyone in the audience not matched by an earlier group"}
		if g.Position > 0 {
			g.ConditionText = segmentation.Describe(g.rules())
		}
		g.Issues = []Issue{}
		for _, l := range g.languageList() {
			v, ok := g.Content[l]
			if !ok || v.empty() {
				if g.Status == "generated" {
					g.Issues = append(g.Issues, Issue{Field: "group", Code: "translation_missing", Blocking: true, Language: l,
						Message: "The " + LanguageName(l) + " version is missing."})
				}
				continue
			}
			if hasBase && v.BaseHash != baseHash {
				g.Stale = true
			}
			for _, i := range Validate(withVersion(base.Content, v), c.facts(), c.Audience.MaxFirstNameLen) {
				i.Language = l
				g.Issues = append(g.Issues, i)
			}
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// reachableMembers lists reachable audience members, grouped by rules.
func (s *Service) reachableMembers(ctx context.Context, c Campaign, groups []segmentation.Alternative) ([]segmentation.Member, error) {
	var out []segmentation.Member
	err := s.aud.Members(ctx, c.Audience.rules(), groups, func(m segmentation.Member) error {
		if m.Reachable() {
			out = append(out, m)
		}
		return nil
	})
	return out, err
}

// requireMode loads the campaign and checks it is an unapproved campaign in
// mode with a base draft.
func (s *Service) requireMode(ctx context.Context, id int64, mode string) (Campaign, Draft, error) {
	v, err := s.Get(ctx, id)
	if err != nil {
		return Campaign{}, Draft{}, err
	}
	c := v.Campaign
	if c.Status == "approved" {
		return c, Draft{}, ErrApproved
	}
	if c.Personalization != mode {
		return c, Draft{}, invalid("switch personalization to %s first", mode)
	}
	base, ok := selectedDraft(c, v.Drafts)
	if !ok {
		return c, Draft{}, invalid("choose the draft to personalize first")
	}
	return c, base, nil
}

// ProposeGroups asks Claude for behavioral groups, validates their rules,
// assigns members deterministically (first match wins, remainder group for
// the rest) and stores the result, replacing earlier proposed groups.
func (s *Service) ProposeGroups(ctx context.Context, id int64) (GroupsNote, error) {
	if !s.acquire(jobKey("groups", id)) {
		return GroupsNote{}, ErrBusy
	}
	defer s.release(jobKey("groups", id))
	c, base, err := s.requireMode(ctx, id, ModeGroups)
	if err != nil {
		return GroupsNote{}, err
	}
	members, err := s.reachableMembers(ctx, c, nil)
	if err != nil {
		return GroupsNote{}, err
	}
	if len(members) < minGroupAudience {
		note := GroupsNote{Outcome: "not_meaningful", Warnings: []string{},
			Message: fmt.Sprintf("Only %d reachable customers: too few to split into groups. Continue with Standard content.", len(members))}
		return note, s.storeGroups(ctx, id, nil, note)
	}
	cat, err := s.aud.Catalog(ctx)
	if err != nil {
		return GroupsNote{}, err
	}

	start := time.Now()
	tr := run{campaignID: id, kind: "groups", source: s.gen.Source(), model: s.gen.Model(), input: map[string]any{"members": len(members)}}
	res, err := s.gen.ProposeGroups(ctx, GroupsRequest{Brief: c.Brief, Base: baseText(c, base), Profile: describeProfile(members, s.now()), FieldGuide: segmentation.FieldGuide(cat)})
	tr.finish(start, res.Usage, err)
	s.record(ctx, tr)
	if err != nil {
		return GroupsNote{}, err
	}
	note := GroupsNote{Outcome: res.Outcome, Message: strings.TrimSpace(res.Message), Warnings: []string{}}
	if res.Outcome == "not_meaningful" {
		if note.Message == "" {
			note.Message = "This audience doesn't split into meaningfully different groups. Continue with Standard content."
		}
		return note, s.storeGroups(ctx, id, nil, note)
	}

	// Validate every proposed group; invalid ones are dropped, not repaired.
	var groups []Group
	for _, p := range res.Groups {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			name = fmt.Sprintf("Group %d", len(groups)+1)
		}
		rules, err := s.aud.ValidateRules(ctx, segmentation.Alternative{Name: name, Reasoning: p.Description, Match: p.Match, Conditions: p.Conditions})
		if err != nil {
			note.Warnings = append(note.Warnings, fmt.Sprintf("“%s” was left out because its rules could not be used.", name))
			continue
		}
		groups = append(groups, Group{Name: name, Description: strings.TrimSpace(p.Description), Match: rules.Match, Conditions: rules.Conditions})
		if len(groups) == maxGroups-1 {
			break
		}
	}
	groups, err = s.sizeGroups(ctx, c, groups, &note)
	if err != nil {
		return note, err
	}
	if len(groups) < 2 {
		note.Outcome = "not_meaningful"
		note.Message = "The proposed groups didn't split this audience into distinct parts. Continue with Standard content, or try again."
		groups = nil
	}
	return note, s.storeGroups(ctx, id, groups, note)
}

// sizeGroups assigns reachable members to groups, drops groups nobody falls
// into and adds the remainder group when it has members.
func (s *Service) sizeGroups(ctx context.Context, c Campaign, groups []Group, note *GroupsNote) ([]Group, error) {
	// Removing a group moves its members to later groups, so re-assign until
	// every remaining rule group is large enough (first-match order is kept).
	for {
		members, err := s.reachableMembers(ctx, c, groupRules(numbered(groups)))
		if err != nil {
			return nil, err
		}
		size := map[int]int{}
		for _, m := range members {
			size[m.Group]++
		}
		var kept []Group
		for i, g := range groups {
			if n := size[i+1]; n > 0 && n < minGroupSize {
				note.Warnings = append(note.Warnings, fmt.Sprintf("“%s” was left out because only %d customers fall into it (at least %d are needed).", g.Name, n, minGroupSize))
				continue
			}
			kept = append(kept, g)
		}
		if len(kept) == len(groups) {
			return s.finishSizing(groups, members, note), nil
		}
		groups = kept
	}
}

func (s *Service) finishSizing(groups []Group, members []segmentation.Member, note *GroupsNote) []Group {
	size := map[int]int{}
	langs := map[int]map[string]int{}
	for _, m := range members {
		size[m.Group]++
		if langs[m.Group] == nil {
			langs[m.Group] = map[string]int{}
		}
		langs[m.Group][ContentLanguage(m.Language)]++
	}
	var kept []Group
	for i, g := range groups {
		if size[i+1] == 0 {
			note.Warnings = append(note.Warnings, fmt.Sprintf("“%s” was left out because no reachable customer falls into it.", g.Name))
			continue
		}
		g.Size, g.Languages = size[i+1], langs[i+1]
		kept = append(kept, g)
	}
	if size[0] > 0 {
		kept = append(kept, Group{Name: "Everyone else", Description: "Customers who don't match any group above.", Match: "all",
			Size: size[0], Languages: langs[0], remainder: true})
	}
	return numbered(kept)
}

// numbered assigns positions 1..n to rule groups (remainder stays 0).
func numbered(gs []Group) []Group {
	n := 0
	for i := range gs {
		if gs[i].remainder {
			gs[i].Position = 0
			continue
		}
		n++
		gs[i].Position = n
	}
	return gs
}

func (s *Service) storeGroups(ctx context.Context, id int64, groups []Group, note GroupsNote) error {
	return s.mutate(ctx, id, func(tx pgx.Tx, c Campaign) error {
		if _, err := tx.Exec(ctx, `DELETE FROM campaign_groups WHERE campaign_id = $1`, id); err != nil {
			return err
		}
		for _, g := range groups {
			conds, _ := json.Marshal(nonNilConds(g.Conditions))
			langs, _ := json.Marshal(g.Languages)
			if _, err := tx.Exec(ctx, `INSERT INTO campaign_groups (campaign_id, position, name, description, match, conditions, size, languages)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, g.Position, g.Name, g.Description, g.Match, conds, g.Size, langs); err != nil {
				return err
			}
		}
		nj, _ := json.Marshal(note)
		_, err := tx.Exec(ctx, `UPDATE campaigns SET groups_note = $2 WHERE id = $1`, id, nj)
		return err
	})
}

func nonNilConds(c []segmentation.Condition) []segmentation.Condition {
	if c == nil {
		return []segmentation.Condition{}
	}
	return c
}

// describeProfile summarizes reachable members for the grouping prompt.
// Only aggregates are sent; no individual customer data.
func describeProfile(ms []segmentation.Member, now time.Time) string {
	n := len(ms)
	pct := func(k int) string { return fmt.Sprintf("%.0f%%", 100*float64(k)/float64(max(n, 1))) }
	var orders [4]int
	var spent, sinceOrder, sinceActive []float64
	var cart30, joined30 int
	langs, cities := map[string]int{}, map[string]int{}
	days := func(t time.Time) float64 { return now.Sub(t).Hours() / 24 }
	for _, m := range ms {
		switch {
		case m.OrderCount == 0:
			orders[0]++
		case m.OrderCount == 1:
			orders[1]++
		case m.OrderCount <= 3:
			orders[2]++
		default:
			orders[3]++
		}
		if m.OrderCount > 0 {
			spent = append(spent, m.TotalSpent)
		}
		if m.LastOrderAt != nil {
			sinceOrder = append(sinceOrder, days(*m.LastOrderAt))
		}
		if m.LastCartAt != nil && days(*m.LastCartAt) <= 30 {
			cart30++
		}
		if days(m.CreatedAt) <= 30 {
			joined30++
		}
		sinceActive = append(sinceActive, days(m.LastActiveAt))
		langs[m.Language]++
		cities[m.City]++
	}
	q := func(v []float64) string {
		if len(v) == 0 {
			return "n/a"
		}
		sort.Float64s(v)
		at := func(p float64) float64 { return v[int(p*float64(len(v)-1))] }
		return fmt.Sprintf("p25 %.0f, median %.0f, p75 %.0f, max %.0f", at(0.25), at(0.5), at(0.75), v[len(v)-1])
	}
	top := func(m map[string]int, k int) string {
		keys := make([]string, 0, len(m))
		for key := range m {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] || (m[keys[i]] == m[keys[j]] && keys[i] < keys[j]) })
		var parts []string
		for i, key := range keys {
			if i == k {
				break
			}
			parts = append(parts, key+" "+pct(m[key]))
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprintf(`Reachable customers: %d
Lifetime orders: none %s, 1 order %s, 2-3 orders %s, 4+ orders %s
Lifetime spend in TRY (buyers only): %s
Days since last order (buyers only): %s
Days since last activity: %s
Added to cart in the last 30 days: %s
Joined in the last 30 days: %s
Languages: %s
Top cities: %s`, n, pct(orders[0]), pct(orders[1]), pct(orders[2]), pct(orders[3]), q(spent), q(sinceOrder), q(sinceActive),
		pct(cart30), pct(joined30), top(langs, 5), top(cities, 5))
}

// Example is a representative member of a group for previews.
type Example struct {
	Label  string              `json:"label"`
	Member segmentation.Member `json:"member"`
}

// GroupExamples returns one representative member of a group: the one
// with the median lifetime spend among its reachable members.
func (s *Service) GroupExamples(ctx context.Context, id int64, position int) ([]Example, error) {
	v, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	members, err := s.reachableMembers(ctx, v.Campaign, groupRules(v.Groups))
	if err != nil {
		return nil, err
	}
	var in []segmentation.Member
	for _, m := range members {
		if m.Group == position {
			in = append(in, m)
		}
	}
	if len(in) == 0 {
		return []Example{}, nil
	}
	sort.SliceStable(in, func(i, j int) bool { return in[i].TotalSpent < in[j].TotalSpent })
	return []Example{{Label: "Typical member", Member: in[len(in)/2]}}, nil
}

// GroupsEstimate is the expected cost of writing all groups' messages.
func (s *Service) GroupsEstimate(ctx context.Context, id int64) (Estimate, error) {
	v, err := s.Get(ctx, id)
	if err != nil {
		return Estimate{}, err
	}
	calls := 0
	for _, g := range v.Groups {
		if g.Size > 0 {
			calls++
		}
	}
	return s.estimate(ctx, "group_content", calls), nil
}

// GenerateGroupContent writes every group's messages in the background, one
// call per group covering only the languages present in that group.
func (s *Service) GenerateGroupContent(ctx context.Context, id int64) error {
	c, base, err := s.requireMode(ctx, id, ModeGroups)
	if err != nil {
		return err
	}
	ds := []Draft{base}
	groups, err := s.groups(ctx, c, ds)
	if err != nil {
		return err
	}
	if len(groups) == 0 {
		return invalid("propose groups first")
	}
	if s.running(jobKey("groups", id)) {
		return ErrBusy
	}
	if err := s.mutate(ctx, id, func(tx pgx.Tx, _ Campaign) error {
		_, err := tx.Exec(ctx, `UPDATE campaign_groups SET status = 'generating', error = NULL, updated_at = now() WHERE campaign_id = $1 AND size > 0`, id)
		return err
	}); err != nil {
		return err
	}
	if !s.start(jobKey("groups", id), func(ctx context.Context) { s.runGroups(ctx, c, base, groups) }) {
		return ErrBusy
	}
	return nil
}

func (s *Service) runGroups(ctx context.Context, c Campaign, base Draft, groups []Group) {
	bt := baseText(c, base)
	hash := hashOf(bt)
	sem := make(chan struct{}, groupWorkers)
	var wg sync.WaitGroup
	for _, g := range groups {
		if g.Size == 0 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			langs := g.languageList()
			start := time.Now()
			pos := g.Position
			tr := run{campaignID: c.ID, kind: "group_content", groupPosition: &pos, source: s.gen.Source(), model: s.gen.Model(),
				input: map[string]any{"group": g.Name, "languages": langs, "base_version": base.Version}}
			res, err := s.gen.GroupContent(ctx, GroupContentRequest{Brief: c.Brief, Base: bt, BaseLanguage: c.BaseLanguage, Name: g.Name,
				Description: g.Description, ConditionText: g.ConditionText, Size: g.Size, Languages: langs, MaxNameLen: c.Audience.MaxFirstNameLen})
			tr.finish(start, res.Usage, err)
			s.record(ctx, tr)
			status, msg := "generated", ""
			content := map[string]Version{}
			if err != nil {
				status, msg = "failed", userError(err)
			} else {
				for l, v := range res.Versions {
					v.BaseHash = hash
					content[l] = v
				}
			}
			cj, _ := json.Marshal(content)
			wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), queryTimeout)
			defer cancel()
			if _, err := s.pool.Exec(wctx, `UPDATE campaign_groups SET status = $3, error = NULLIF($4, ''),
				content = CASE WHEN $3 = 'generated' THEN $5::jsonb ELSE content END, updated_at = now()
				WHERE campaign_id = $1 AND position = $2`, c.ID, g.Position, status, msg, cj); err != nil {
				s.log.Error("could not store group content", "campaign", c.ID, "group", g.Position, "error", err)
			}
		}()
	}
	wg.Wait()
}

// SaveGroupContent stores the marketer's edits of a group's messages.
func (s *Service) SaveGroupContent(ctx context.Context, id int64, position int, content map[string]Version) error {
	for _, v := range content {
		if len(v.SMSBody)+len(v.EmailSubject)+len(v.EmailBody) > maxFieldLen {
			return invalid("message text is too long")
		}
	}
	v, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	base, ok := selectedDraft(v.Campaign, v.Drafts)
	if !ok {
		return invalid("choose the draft to personalize first")
	}
	hash := hashOf(baseText(v.Campaign, base))
	return s.mutate(ctx, id, func(tx pgx.Tx, c Campaign) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT content FROM campaign_groups WHERE campaign_id = $1 AND position = $2 FOR UPDATE`, id, position).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		cur := map[string]Version{}
		_ = json.Unmarshal(raw, &cur)
		for l, nv := range content {
			old, had := cur[l]
			nv.BaseHash = old.BaseHash
			if !had || old.texts() != nv.texts() {
				nv.BaseHash = hash
			}
			cur[l] = nv
		}
		cj, _ := json.Marshal(cur)
		_, err = tx.Exec(ctx, `UPDATE campaign_groups SET content = $3, status = 'generated', error = NULL, updated_at = now()
			WHERE campaign_id = $1 AND position = $2`, id, position, cj)
		return err
	})
}

// userError is a short marketer-facing reason for a failed generation.
func userError(err error) string {
	switch statusOf(err) {
	case "unavailable":
		return "The AI service was busy or timed out."
	case "bad_reply":
		return "The AI returned an unusable answer."
	case "refusal":
		return "The AI declined to write this message."
	case "canceled":
		return "Generation was stopped."
	case "auth_error":
		return "The AI service rejected the API key."
	}
	return "Generation failed."
}
