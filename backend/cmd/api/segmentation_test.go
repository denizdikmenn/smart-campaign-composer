package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smart-campaign-composer/backend/internal/segmentation"
)

type fakeSuggester struct{ resp segmentation.Response }

func (f fakeSuggester) Suggest(context.Context, string) (segmentation.Response, error) {
	return f.resp, nil
}
func (fakeSuggester) Preview(context.Context, segmentation.Alternative, int) ([]segmentation.PreviewCustomer, error) {
	return nil, nil
}

func TestSuggestContract(t *testing.T) {
	unsupported := segmentation.Response{Outcome: segmentation.OutcomeUnsupported, Message: "No subscription data.",
		Unsupported: []string{"subscriptions"}, Questions: []string{}, Suggestions: []string{}, Alternatives: []segmentation.Result{}, Warnings: []string{}}
	h := newHandler(slog.New(slog.DiscardHandler), fakeDB{}, nil, fakeSuggester{unsupported})

	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/segmentation/suggest", strings.NewReader(body)))
		return rec
	}
	if rec := post(`{"brief":"short"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("short brief: %d", rec.Code)
	}
	rec := post(`{"brief":"Email US customers who cancelled subscriptions"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	for _, k := range []string{"outcome", "message", "questions", "unsupported_requirements", "suggestions", "alternatives", "warnings", "total_customers"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %q in response", k)
		}
	}
	if got["outcome"] != "unsupported" {
		t.Errorf("outcome %v", got["outcome"])
	}
}
