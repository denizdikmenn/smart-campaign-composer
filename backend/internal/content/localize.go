package content

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"sort"
)

// Content languages. A customer whose language is not supported receives the
// FallbackLanguage version; this is the only fallback rule.
var SupportedLanguages = []string{"tr", "en"}

const FallbackLanguage = "en"

var languageNames = map[string]string{"tr": "Turkish", "en": "English"}

// LanguageName is the English name of a supported content language.
func LanguageName(l string) string {
	if n, ok := languageNames[l]; ok {
		return n
	}
	return l
}

// ContentLanguage maps a customer language to the content language they get.
func ContentLanguage(l string) string {
	if slices.Contains(SupportedLanguages, l) {
		return l
	}
	return FallbackLanguage
}

// contentLanguages folds reachable members per customer language into
// counts per content language.
func contentLanguages(raw map[string]int) map[string]int {
	out := map[string]int{}
	for l, n := range raw {
		if n > 0 {
			out[ContentLanguage(l)] += n
		}
	}
	return out
}

// LanguageList returns the content languages present in the audience, the
// base (most common) language first. An audience without language data
// (campaigns created before localization) is treated as base-only.
func (a Audience) LanguageList() []string {
	langs := make([]string, 0, len(a.Languages))
	for l, n := range a.Languages {
		if n > 0 {
			langs = append(langs, l)
		}
	}
	sort.Slice(langs, func(i, j int) bool {
		ni, nj := a.Languages[langs[i]], a.Languages[langs[j]]
		if ni != nj {
			return ni > nj
		}
		return slices.Index(SupportedLanguages, langs[i]) < slices.Index(SupportedLanguages, langs[j])
	})
	if len(langs) == 0 {
		return []string{FallbackLanguage}
	}
	return langs
}

// BaseLanguage is the language drafts are written in.
func (a Audience) BaseLanguage() string { return a.LanguageList()[0] }

// Version is one language version of a message set. BaseHash records which
// base text a translation (or group text) was written from, to detect staleness.
type Version struct {
	SMSBody      string `json:"sms_body"`
	EmailSubject string `json:"email_subject"`
	EmailBody    string `json:"email_body"`
	BaseHash     string `json:"base_hash,omitempty"`
}

func (v Version) texts() string { return v.SMSBody + "\n" + v.EmailSubject + "\n" + v.EmailBody }

func (v Version) empty() bool { return v.SMSBody == "" && v.EmailSubject == "" && v.EmailBody == "" }

// hashOf identifies a set of message texts.
func hashOf(c Content) string {
	sum := sha256.Sum256([]byte(c.SMSBody + "\x00" + c.EmailSubject + "\x00" + c.EmailBody))
	return hex.EncodeToString(sum[:8])
}

// withVersion returns c with the message texts of v (settings and channels kept).
func withVersion(c Content, v Version) Content {
	c.SMSBody, c.EmailSubject, c.EmailBody = v.SMSBody, v.EmailSubject, v.EmailBody
	return c
}

// LangStatus is the state of one required language of a draft.
type LangStatus struct {
	Language string `json:"language"`
	Status   string `json:"status"` // base | ok | stale | missing
}

// TextIn returns the draft's text in lang and whether it exists.
func (d Draft) TextIn(lang string) (Version, bool) {
	if lang == d.Language {
		return Version{SMSBody: d.SMSBody, EmailSubject: d.EmailSubject, EmailBody: d.EmailBody}, true
	}
	v, ok := d.Translations[lang]
	return v, ok && !v.empty()
}

func (d Draft) localization(langs []string) []LangStatus {
	out := []LangStatus{}
	base := hashOf(d.Content)
	for _, l := range langs {
		st := "ok"
		switch v, ok := d.TextIn(l); {
		case l == d.Language:
			st = "base"
		case !ok:
			st = "missing"
		case v.BaseHash != base:
			st = "stale"
		}
		out = append(out, LangStatus{Language: l, Status: st})
	}
	return out
}

// ValidateDraft validates the base text and every required language version.
// Missing translations block approval; stale ones (the base text changed
// after translating) are warnings.
func ValidateDraft(d Draft, brief string, maxNameLen int, langs []string) []Issue {
	issues := []Issue{}
	for _, ls := range d.localization(langs) {
		switch ls.Status {
		case "missing":
			issues = append(issues, Issue{Field: "translations", Code: "translation_missing", Blocking: true, Language: ls.Language,
				Message: "The " + LanguageName(ls.Language) + " version is missing. Generate translations."})
			continue
		case "stale":
			issues = append(issues, Issue{Field: "translations", Code: "translation_stale", Language: ls.Language,
				Message: "The " + LanguageName(ls.Language) + " version was translated from an earlier text. Update translations or review it."})
		}
		v, _ := d.TextIn(ls.Language)
		for _, i := range Validate(withVersion(d.Content, v), brief, maxNameLen) {
			i.Language = ls.Language
			issues = append(issues, i)
		}
	}
	return issues
}
