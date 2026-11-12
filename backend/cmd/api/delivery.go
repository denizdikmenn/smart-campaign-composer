package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/smart-campaign-composer/backend/internal/content"
	"github.com/smart-campaign-composer/backend/internal/delivery"
)

// deliveryHandler starts sending (job creation only: the worker process
// sends) and serves monitoring, all straight from PostgreSQL.
type deliveryHandler struct {
	log  *slog.Logger
	pool *pgxpool.Pool
	src  delivery.MessageSource
}

func (h *deliveryHandler) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/campaigns", h.list)
	mux.HandleFunc("POST /api/campaigns/{id}/send", h.send)
	mux.HandleFunc("GET /api/campaigns/{id}/delivery", h.report)
	mux.HandleFunc("GET /api/campaigns/{id}/messages", h.messages)
	mux.HandleFunc("GET /api/campaigns/{id}/messages/{mid}", h.message)
	mux.HandleFunc("POST /api/campaigns/{id}/messages/{mid}/retry", h.retry)
	mux.HandleFunc("POST /api/campaigns/{id}/retry-failed", h.retry)
	mux.HandleFunc("GET /api/metrics", h.metrics)
}

var messageStatuses = map[string]bool{"": true, "pending": true, "retrying": true, "sent": true, "failed": true, "skipped": true}

// GET /api/campaigns/{id}/messages?status=&channel=&page=&page_size=: recipient-level deliveries.
func (h *deliveryHandler) messages(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	q := r.URL.Query()
	f := delivery.MessageFilter{Status: q.Get("status"), Channel: q.Get("channel")}
	var err error
	if f.Page, err = intParam(q.Get("page"), 1, 1, 100000); err != nil {
		writeError(w, http.StatusBadRequest, "invalid page")
		return
	}
	if f.PageSize, err = intParam(q.Get("page_size"), 25, 1, 100); err != nil {
		writeError(w, http.StatusBadRequest, "invalid page_size")
		return
	}
	if !messageStatuses[f.Status] || (f.Channel != "" && f.Channel != "sms" && f.Channel != "email") {
		writeError(w, http.StatusBadRequest, "invalid status or channel")
		return
	}
	p, err := delivery.Messages(r.Context(), h.pool, id, f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// GET /api/campaigns/{id}/messages/{mid}: one delivery with its content and attempt history.
func (h *deliveryHandler) message(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	mid, ok := pathInt(w, r, "mid")
	if !ok {
		return
	}
	d, err := delivery.GetMessage(r.Context(), h.pool, id, mid)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// POST /api/campaigns/{id}/messages/{mid}/retry and POST /api/campaigns/{id}/retry-failed:
// requeue failed deliveries that did not fail permanently (same rows and keys;
// the worker re-checks consent and applies the normal retry policy).
func (h *deliveryHandler) retry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	var mid int64
	if r.PathValue("mid") != "" {
		if mid, ok = pathInt(w, r, "mid"); !ok {
			return
		}
	}
	res, err := delivery.RetryFailed(r.Context(), h.pool, id, mid)
	var nr *delivery.NotRetryableError
	if errors.As(err, &nr) {
		writeError(w, http.StatusConflict, "This message can't be retried: "+nr.Reason+".")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if res.Requeued > 0 {
		h.log.Info("manual retry", "campaign", id, "delivery", mid, "requeued", res.Requeued)
	}
	writeJSON(w, http.StatusOK, res)
}

// approvedMessages adapts the content service's resolver to the delivery queue.
func approvedMessages(svc *content.Service) delivery.MessageSource {
	return func(ctx context.Context, id int64, fn func(delivery.Message) error) error {
		err := svc.ApprovedMessages(ctx, id, func(m content.Message) error {
			return fn(delivery.Message{CustomerID: m.CustomerID, Channel: m.Channel, VariantPosition: m.VariantPosition,
				GroupPosition: m.GroupPosition, Language: m.Language, Source: m.Source, Subject: m.Subject, Body: m.Body, Skip: m.Skip})
		})
		if errors.Is(err, content.ErrNotApproved) {
			return delivery.ErrNotApproved
		}
		return err
	}
}

// POST /api/campaigns/{id}/send: creates the delivery jobs once and returns
// at once (202). Repeating it returns the current state and creates nothing.
func (h *deliveryHandler) send(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	created, err := delivery.Dispatch(r.Context(), h.pool, id, h.src)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if created > 0 {
		h.log.Info("campaign dispatched", "campaign", id, "jobs", created)
	}
	h.report(w, r)
}

// GET /api/campaigns/{id}/delivery
func (h *deliveryHandler) report(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(w, r, "id")
	if !ok {
		return
	}
	rep, err := delivery.CampaignReport(r.Context(), h.pool, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusAccepted
	}
	writeJSON(w, status, rep)
}

// GET /api/metrics: queue depth and delivery totals across campaigns.
func (h *deliveryHandler) metrics(w http.ResponseWriter, r *http.Request) {
	m, err := delivery.GetMetrics(r.Context(), h.pool)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// GET /api/campaigns: the 20 most recent campaigns with delivery progress.
func (h *deliveryHandler) list(w http.ResponseWriter, r *http.Request) {
	cs, err := delivery.RecentCampaigns(r.Context(), h.pool, 20)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaigns": cs})
}

func (h *deliveryHandler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return
	}
	switch {
	case errors.Is(err, delivery.ErrNotFound), errors.Is(err, content.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, delivery.ErrNotApproved), errors.Is(err, delivery.ErrNoRecipients):
		writeError(w, http.StatusConflict, err.Error())
	default:
		h.log.Error("delivery request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
