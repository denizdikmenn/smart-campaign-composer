package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

const (
	minBriefLen    = 10
	maxBriefLen    = 2000
	suggestTimeout = 60 * time.Second // LLM call + counts; exceeds the server-wide WriteTimeout
)

type suggester interface {
	Suggest(ctx context.Context, brief string) (segmentation.Response, error)
	Preview(ctx context.Context, a segmentation.Alternative, limit int) ([]segmentation.PreviewCustomer, error)
}

type segmentationHandler struct {
	log *slog.Logger
	svc suggester
}

// suggest: POST /api/segmentation/suggest {"brief": "..."}
func (h segmentationHandler) suggest(w http.ResponseWriter, r *http.Request) {
	// The server-wide WriteTimeout is shorter than an LLM round trip.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(suggestTimeout + 5*time.Second))

	var req struct {
		Brief string `json:"brief"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	brief := strings.TrimSpace(req.Brief)
	if n := utf8.RuneCountInString(brief); n < minBriefLen || n > maxBriefLen {
		writeError(w, http.StatusBadRequest, "brief must be between 10 and 2000 characters")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), suggestTimeout)
	defer cancel()
	resp, err := h.svc.Suggest(ctx, brief)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, resp)
	case r.Context().Err() != nil:
		// client went away
	case errors.Is(err, segmentation.ErrRefusal):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, segmentation.ErrAuth):
		h.log.Error("anthropic auth failed")
		writeError(w, http.StatusBadGateway, "The AI service rejected the configured API key.")
	case errors.Is(err, segmentation.ErrRetry), errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusServiceUnavailable, "The AI service is busy or timed out. Please retry.")
	case errors.Is(err, segmentation.ErrBadReply):
		h.log.Warn("unusable AI reply", "error", err)
		writeError(w, http.StatusBadGateway, "The AI returned an unusable answer. Please retry.")
	default:
		h.log.Error("suggest failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// preview: POST /api/segmentation/preview {"match": "...", "conditions": [...]}
// returns a handful of matching customers. Rules are re-validated server-side.
func (h segmentationHandler) preview(w http.ResponseWriter, r *http.Request) {
	var a segmentation.Alternative
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&a); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	list, err := h.svc.Preview(ctx, a, 12)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"customers": list})
	case errors.Is(err, segmentation.ErrInvalidRules):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		h.log.Error("preview failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
