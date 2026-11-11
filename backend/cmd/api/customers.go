package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/smart-campaign-composer/backend/internal/customers"
)

const (
	queryTimeout   = 5 * time.Second
	defaultPerPage = 25
	maxPerPage     = 100
	maxPage        = 1_000_000
	maxCityLen     = 64 // also bounds category and segment
)

type customerStore interface {
	Summary(ctx context.Context, now time.Time) (customers.Summary, error)
	List(ctx context.Context, f customers.Filter, now time.Time, limit, offset int) ([]customers.Customer, int, error)
	Get(ctx context.Context, id int64) (customers.CustomerDetail, error)
	Audiences(ctx context.Context, now time.Time, category string) (customers.AudienceReport, error)
}

type customerHandler struct {
	log   *slog.Logger
	store customerStore
}

type customerList struct {
	Items    []customers.Customer `json:"items"`
	Total    int                  `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
}

func (h customerHandler) summary(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	sum, err := h.store.Summary(ctx, time.Now())
	if err != nil {
		h.internalError(w, "customer summary", err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (h customerHandler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page, err := intParam(q.Get("page"), 1, 1, maxPage)
	if err != nil {
		writeError(w, http.StatusBadRequest, "page: "+err.Error())
		return
	}
	pageSize, err := intParam(q.Get("page_size"), defaultPerPage, 1, maxPerPage)
	if err != nil {
		writeError(w, http.StatusBadRequest, "page_size: "+err.Error())
		return
	}
	f := customers.Filter{City: strings.TrimSpace(q.Get("city"))}
	if len(f.City) > maxCityLen {
		writeError(w, http.StatusBadRequest, "city: too long")
		return
	}
	if f.EmailConsent, err = boolParam(q.Get("email_consent")); err != nil {
		writeError(w, http.StatusBadRequest, "email_consent: "+err.Error())
		return
	}
	if f.SMSConsent, err = boolParam(q.Get("sms_consent")); err != nil {
		writeError(w, http.StatusBadRequest, "sms_consent: "+err.Error())
		return
	}

	f.Segment = q.Get("segment")
	f.Category = strings.TrimSpace(q.Get("category"))
	if len(f.Segment) > maxCityLen || len(f.Category) > maxCityLen {
		writeError(w, http.StatusBadRequest, "segment/category: too long")
		return
	}
	if err := customers.ValidateSegment(f.Segment, f.Category); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if f.Sort, err = customers.ParseSort(q.Get("sort"), q.Get("order")); err != nil {
		writeError(w, http.StatusBadRequest, "sort: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	items, total, err := h.store.List(ctx, f, time.Now(), pageSize, (page-1)*pageSize)
	if err != nil {
		h.internalError(w, "customer list", err)
		return
	}
	writeJSON(w, http.StatusOK, customerList{Items: items, Total: total, Page: page, PageSize: pageSize})
}

func (h customerHandler) get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "id: must be a positive integer")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	d, err := h.store.Get(ctx, id)
	if errors.Is(err, customers.ErrNotFound) {
		writeError(w, http.StatusNotFound, "customer not found")
		return
	}
	if err != nil {
		h.internalError(w, "customer detail", err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h customerHandler) audiences(w http.ResponseWriter, r *http.Request) {
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	if len(category) > maxCityLen {
		writeError(w, http.StatusBadRequest, "category: too long")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), queryTimeout)
	defer cancel()

	rep, err := h.store.Audiences(ctx, time.Now(), category)
	if err != nil {
		h.internalError(w, "audiences", err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (h customerHandler) internalError(w http.ResponseWriter, what string, err error) {
	h.log.Error(what, "error", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// intParam parses an optional integer in [min, max]; empty means def.
func intParam(s string, def, min, max int) (int, error) {
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("must be an integer")
	}
	if n < min || n > max {
		return 0, fmt.Errorf("must be between %d and %d", min, max)
	}
	return n, nil
}

// boolParam parses an optional "true"/"false"; empty means no filter.
func boolParam(s string) (*bool, error) {
	switch s {
	case "":
		return nil, nil
	case "true", "false":
		b := s == "true"
		return &b, nil
	}
	return nil, fmt.Errorf("must be true or false")
}
