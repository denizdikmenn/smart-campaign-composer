package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

type runReader interface {
	ListRuns(ctx context.Context, status string, limit int) ([]segmentation.Run, error)
	GetRun(ctx context.Context, id int64) (segmentation.Run, error)
}

type adminHandler struct {
	log  *slog.Logger
	runs runReader
}

// GET /api/admin/segmentation/runs?status=&limit=
func (h adminHandler) list(w http.ResponseWriter, r *http.Request) {
	limit, err := intParam(r.URL.Query().Get("limit"), 20, 1, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, "limit "+err.Error())
		return
	}
	runs, err := h.runs.ListRuns(r.Context(), r.URL.Query().Get("status"), limit)
	if err != nil {
		h.log.Error("list runs failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// GET /api/admin/segmentation/runs/{id}
func (h adminHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	run, err := h.runs.GetRun(r.Context(), id)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, run)
	case errors.Is(err, segmentation.ErrRunNotFound):
		writeError(w, http.StatusNotFound, "run not found")
	default:
		h.log.Error("get run failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
