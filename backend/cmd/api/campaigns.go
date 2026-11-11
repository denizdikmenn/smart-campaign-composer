package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/smart-campaign-composer/backend/internal/content"
	"github.com/smart-campaign-composer/backend/internal/llm"
)

const generateTimeout = 90 * time.Second

type campaignService interface {
	Create(ctx context.Context, brief string, a content.Audience) (content.View, error)
	Get(ctx context.Context, id int64) (content.View, error)
	Generate(ctx context.Context, id int64, in content.GenerateInput, emit func(content.Event)) (content.Draft, error)
	CreateDraft(ctx context.Context, id int64, fromVersion int) (content.Draft, error)
	SaveDraft(ctx context.Context, id int64, version int, e content.DraftEdit) (content.Draft, error)
	Localize(ctx context.Context, id int64, version int) (content.Draft, error)
	SelectDraft(ctx context.Context, id int64, version int) error
	SetVariants(ctx context.Context, id int64, versions []int) error
	SetPersonalization(ctx context.Context, id int64, mode string, base int) error
	ProposeGroups(ctx context.Context, id int64) (content.GroupsNote, error)
	GroupExamples(ctx context.Context, id int64, position int) ([]content.Example, error)
	GroupsEstimate(ctx context.Context, id int64) (content.Estimate, error)
	GenerateGroupContent(ctx context.Context, id int64) error
	SaveGroupContent(ctx context.Context, id int64, position int, c map[string]content.Version) error
	Candidates(ctx context.Context, id int64, limit int) ([]content.Candidate, error)
	Sample(ctx context.Context, id, customerID int64) (content.SampleResult, error)
	IndividualEstimate(ctx context.Context, id int64) (content.SampleResult, error)
	ApplyIndividual(ctx context.Context, id int64) error
	StopIndividual(id int64)
	Review(ctx context.Context, id int64, version int) (content.Review, error)
	Approve(ctx context.Context, id int64, version int) (content.View, error)
}

type campaignHandler struct {
	log *slog.Logger
	svc campaignService
}

func (h campaignHandler) routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/campaigns", h.create)
	mux.HandleFunc("GET /api/campaigns/{id}", h.get)
	mux.HandleFunc("POST /api/campaigns/{id}/generate", h.generate)
	mux.HandleFunc("POST /api/campaigns/{id}/drafts", h.createDraft)
	mux.HandleFunc("PUT /api/campaigns/{id}/drafts/{version}", h.saveDraft)
	mux.HandleFunc("POST /api/campaigns/{id}/drafts/{version}/localize", h.localize)
	mux.HandleFunc("PUT /api/campaigns/{id}/selection", h.selection)
	mux.HandleFunc("PUT /api/campaigns/{id}/variants", h.variants)
	mux.HandleFunc("PUT /api/campaigns/{id}/personalization", h.personalization)
	mux.HandleFunc("POST /api/campaigns/{id}/groups/propose", h.proposeGroups)
	mux.HandleFunc("GET /api/campaigns/{id}/groups/estimate", h.groupsEstimate)
	mux.HandleFunc("POST /api/campaigns/{id}/groups/generate", h.generateGroups)
	mux.HandleFunc("GET /api/campaigns/{id}/groups/{position}/examples", h.groupExamples)
	mux.HandleFunc("PUT /api/campaigns/{id}/groups/{position}", h.saveGroup)
	mux.HandleFunc("GET /api/campaigns/{id}/individual/candidates", h.candidates)
	mux.HandleFunc("GET /api/campaigns/{id}/individual/estimate", h.individualEstimate)
	mux.HandleFunc("POST /api/campaigns/{id}/individual/sample", h.sample)
	mux.HandleFunc("POST /api/campaigns/{id}/individual/apply", h.applyIndividual)
	mux.HandleFunc("POST /api/campaigns/{id}/individual/stop", h.stopIndividual)
	mux.HandleFunc("GET /api/campaigns/{id}/review", h.review)
	mux.HandleFunc("POST /api/campaigns/{id}/approve", h.approve)
}

// POST /api/campaigns {"brief": "...", "audience": {name, match, conditions}}
func (h campaignHandler) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Brief    string           `json:"brief"`
		Audience content.Audience `json:"audience"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	v, err := h.svc.Create(r.Context(), req.Brief, req.Audience)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

// GET /api/campaigns/{id}
func (h campaignHandler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	v, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// PUT /api/campaigns/{id}/drafts/{version} with the full edited content.
func (h campaignHandler) saveDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	version, ok := pathInt(w, r, "version")
	if !ok {
		return
	}
	var c content.DraftEdit
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	d, err := h.svc.SaveDraft(r.Context(), id, int(version), c)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"draft": d})
}

// POST /api/campaigns/{id}/approve {"version": n}: n is the Standard draft (0 when variants are active).
func (h campaignHandler) approve(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Version int `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.Version < 0 {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}
	v, err := h.svc.Approve(r.Context(), id, req.Version)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// POST /api/campaigns/{id}/generate {"from_version": 0|n, "feedback": "..."}
//
// Server-sent events: "field" {field, value} while the model writes, then
// either "done" {draft} or "error" {message, retryable}. Errors that happen
// before anything was streamed (unknown campaign, approved, busy) are plain
// JSON errors with an HTTP status instead.
func (h campaignHandler) generate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var req content.GenerateInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil || req.FromVersion < 0 {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(generateTimeout + 5*time.Second))
	ctx, cancel := context.WithTimeout(r.Context(), generateTimeout)
	defer cancel()

	sse := &sseWriter{w: w, rc: rc}
	d, err := h.svc.Generate(ctx, id, req, func(ev content.Event) {
		sse.send(ev.Type, ev)
	})
	switch {
	case err == nil:
		sse.send("done", map[string]any{"draft": d})
	case r.Context().Err() != nil:
		// client went away; nothing to write
	case !sse.started:
		h.fail(w, r, err)
	default:
		status, msg := h.classify(err)
		sse.send("error", map[string]any{"message": msg, "retryable": status == http.StatusServiceUnavailable || status == http.StatusBadGateway})
	}
}

// sseWriter starts the event stream lazily, so early failures can still use
// a normal HTTP status.
type sseWriter struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	started bool
}

func (s *sseWriter) send(event string, v any) {
	if !s.started {
		h := s.w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Accel-Buffering", "no") // nginx: do not buffer the stream
		s.w.WriteHeader(http.StatusOK)
		s.started = true
	}
	data, _ := json.Marshal(v)
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, data)
	_ = s.rc.Flush()
}

func (h campaignHandler) classify(err error) (int, string) {
	var verr *content.ValidationError
	switch {
	case errors.As(err, &verr):
		return http.StatusUnprocessableEntity, verr.Error()
	case errors.Is(err, content.ErrNotFound):
		return http.StatusNotFound, "not found"
	case errors.Is(err, content.ErrApproved), errors.Is(err, content.ErrBusy):
		return http.StatusConflict, err.Error()
	case errors.Is(err, content.ErrInvalid):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, llm.ErrRefusal):
		return http.StatusUnprocessableEntity, "The AI declined to write this campaign. Try rephrasing the brief or feedback."
	case errors.Is(err, llm.ErrAuth):
		h.log.Error("anthropic auth failed")
		return http.StatusBadGateway, "The AI service rejected the configured API key."
	case errors.Is(err, llm.ErrRetry), errors.Is(err, context.DeadlineExceeded):
		h.log.Warn("content generation unavailable", "error", err)
		return http.StatusServiceUnavailable, "The AI service is busy, timed out or the stream was interrupted. Please retry."
	case errors.Is(err, llm.ErrBadReply):
		h.log.Warn("unusable AI reply", "error", err)
		return http.StatusBadGateway, "The AI returned an unusable draft. Please retry."
	default:
		h.log.Error("campaign request failed", "error", err)
		return http.StatusInternalServerError, "internal error"
	}
}

func (h campaignHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return
	}
	status, msg := h.classify(err)
	var verr *content.ValidationError
	if errors.As(err, &verr) {
		writeJSON(w, status, map[string]any{"error": msg, "issues": verr.Issues})
		return
	}
	writeError(w, status, msg)
}

func pathInt(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || v <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+name)
		return 0, false
	}
	return v, true
}

// decode reads a small JSON body into v.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// respondView answers a successful mutation with the updated campaign.
func (h campaignHandler) respondView(w http.ResponseWriter, r *http.Request, id int64, err error) {
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// longCall extends the write deadline for a synchronous model call.
func longCall(w http.ResponseWriter, r *http.Request) (context.Context, context.CancelFunc) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(generateTimeout + 5*time.Second))
	return context.WithTimeout(r.Context(), generateTimeout)
}

// POST /api/campaigns/{id}/drafts {"from_version": n}: duplicate n, or a blank draft without it.
func (h campaignHandler) createDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		FromVersion int `json:"from_version"`
	}
	if !decode(w, r, &req) {
		return
	}
	d, err := h.svc.CreateDraft(r.Context(), id, req.FromVersion)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"draft": d})
}

// POST /api/campaigns/{id}/drafts/{version}/localize: (re)translate missing or stale languages.
func (h campaignHandler) localize(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	version, ok := pathInt(w, r, "version")
	if !ok {
		return
	}
	ctx, cancel := longCall(w, r)
	defer cancel()
	d, err := h.svc.Localize(ctx, id, int(version))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"draft": d})
}

// PUT /api/campaigns/{id}/selection {"version": n}
func (h campaignHandler) selection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Version int `json:"version"`
	}
	if !decode(w, r, &req) {
		return
	}
	h.respondView(w, r, id, h.svc.SelectDraft(r.Context(), id, req.Version))
}

// PUT /api/campaigns/{id}/variants {"versions": [a, b, c]}; [] disables A/B testing.
func (h campaignHandler) variants(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Versions []int `json:"versions"`
	}
	if !decode(w, r, &req) {
		return
	}
	h.respondView(w, r, id, h.svc.SetVariants(r.Context(), id, req.Versions))
}

// PUT /api/campaigns/{id}/personalization {"mode": "standard|groups|individual", "base_version": n}
func (h campaignHandler) personalization(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Mode        string `json:"mode"`
		BaseVersion int    `json:"base_version"`
	}
	if !decode(w, r, &req) {
		return
	}
	h.respondView(w, r, id, h.svc.SetPersonalization(r.Context(), id, req.Mode, req.BaseVersion))
}

// POST /api/campaigns/{id}/groups/propose: one model call, then validation and deterministic sizing.
func (h campaignHandler) proposeGroups(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	ctx, cancel := longCall(w, r)
	defer cancel()
	if _, err := h.svc.ProposeGroups(ctx, id); err != nil {
		h.fail(w, r, err)
		return
	}
	h.respondView(w, r, id, nil)
}

func (h campaignHandler) groupsEstimate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	e, err := h.svc.GroupsEstimate(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// POST /api/campaigns/{id}/groups/generate: starts background generation (202).
func (h campaignHandler) generateGroups(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	if err := h.svc.GenerateGroupContent(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (h campaignHandler) groupExamples(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	pos, err := strconv.Atoi(r.PathValue("position"))
	if err != nil || pos < 0 {
		writeError(w, http.StatusBadRequest, "invalid position")
		return
	}
	ex, err := h.svc.GroupExamples(r.Context(), id, pos)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"examples": ex})
}

// PUT /api/campaigns/{id}/groups/{position} {"content": {lang: {...}}}
func (h campaignHandler) saveGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	pos, err := strconv.Atoi(r.PathValue("position"))
	if err != nil || pos < 0 {
		writeError(w, http.StatusBadRequest, "invalid position")
		return
	}
	var req struct {
		Content map[string]content.Version `json:"content"`
	}
	if !decode(w, r, &req) {
		return
	}
	h.respondView(w, r, id, h.svc.SaveGroupContent(r.Context(), id, pos, req.Content))
}

func (h campaignHandler) candidates(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	cs, err := h.svc.Candidates(r.Context(), id, 30)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": cs})
}

func (h campaignHandler) individualEstimate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	res, err := h.svc.IndividualEstimate(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// POST /api/campaigns/{id}/individual/sample {"customer_id": n}
func (h campaignHandler) sample(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		CustomerID int64 `json:"customer_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	ctx, cancel := longCall(w, r)
	defer cancel()
	res, err := h.svc.Sample(ctx, id, req.CustomerID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// POST /api/campaigns/{id}/individual/apply: explicit "Apply to all"; runs in the background (202).
func (h campaignHandler) applyIndividual(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	if err := h.svc.ApplyIndividual(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (h campaignHandler) stopIndividual(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	h.svc.StopIndividual(id)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
}

// GET /api/campaigns/{id}/review?version=n: consistency gate, recipient plan and cost.
func (h campaignHandler) review(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	version, _ := strconv.Atoi(r.URL.Query().Get("version"))
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	rev, err := h.svc.Review(ctx, id, version)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rev)
}
