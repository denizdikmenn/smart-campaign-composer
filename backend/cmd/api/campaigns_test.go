package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smart-campaign-composer/backend/internal/content"
	"github.com/smart-campaign-composer/backend/internal/llm"
)

type fakeCampaigns struct {
	campaignService // unimplemented methods panic; tests only call the ones below
	events          []content.Event
	genErr          error
	approveEr       error
}

func (fakeCampaigns) Create(context.Context, string, content.Audience) (content.View, error) {
	return content.View{}, nil
}
func (fakeCampaigns) Get(context.Context, int64) (content.View, error) { return content.View{}, nil }
func (f fakeCampaigns) Generate(_ context.Context, _ int64, _ content.GenerateInput, emit func(content.Event)) (content.Draft, error) {
	for _, e := range f.events {
		emit(e)
	}
	if f.genErr != nil {
		return content.Draft{}, f.genErr
	}
	return content.Draft{Version: 1, Issues: []content.Issue{}}, nil
}
func (f fakeCampaigns) SetVariants(context.Context, int64, []int) error { return f.approveEr }
func (f fakeCampaigns) Approve(context.Context, int64, int) (content.View, error) {
	return content.View{}, f.approveEr
}

func serve(f fakeCampaigns, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h := newHandler(slog.New(slog.DiscardHandler), fakeDB{}, nil, nil, f)
	h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// sseEvents returns the event names in order.
func sseEvents(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if name, ok := strings.CutPrefix(line, "event: "); ok {
			out = append(out, name)
		}
	}
	return out
}

func TestGenerateStream(t *testing.T) {
	fields := []content.Event{{Type: "field", Field: "tone", Value: "friendly"}, {Type: "field", Field: "sms_body", Value: "Hi"},
		{Type: "field", Field: "sms_body", Value: "Hi {{first_name}}"}, {Type: "localizing", Languages: []string{"en"}}}

	rec := serve(fakeCampaigns{events: fields}, http.MethodPost, "/api/campaigns/1/generate", `{"from_version":0}`)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d, %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if got := strings.Join(sseEvents(rec.Body.String()), ","); got != "field,field,field,localizing,done" {
		t.Fatalf("events %s\n%s", got, rec.Body)
	}

	// Failure after streaming started: an error event, never "done".
	rec = serve(fakeCampaigns{events: fields[:1], genErr: content.ErrInterrupted}, http.MethodPost, "/api/campaigns/1/generate", `{}`)
	body := rec.Body.String()
	if got := strings.Join(sseEvents(body), ","); got != "field,error" || !strings.Contains(body, `"retryable":true`) {
		t.Fatalf("events %s\n%s", got, body)
	}

	// Failure before anything streamed: a normal HTTP error.
	for err, status := range map[error]int{content.ErrApproved: 409, content.ErrBusy: 409, content.ErrNotFound: 404, llm.ErrAuth: 502} {
		rec = serve(fakeCampaigns{genErr: err}, http.MethodPost, "/api/campaigns/1/generate", `{}`)
		if rec.Code != status || rec.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%v: %d %s", err, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

func TestApproveStatus(t *testing.T) {
	if rec := serve(fakeCampaigns{}, http.MethodPost, "/api/campaigns/1/approve", `{"version":-1}`); rec.Code != 400 {
		t.Fatalf("bad version: %d", rec.Code)
	}
	if rec := serve(fakeCampaigns{approveEr: content.ErrApproved}, http.MethodPost, "/api/campaigns/1/approve", `{"version":1}`); rec.Code != 409 {
		t.Fatalf("approved: %d", rec.Code)
	}
	verr := &content.ValidationError{Issues: []content.Issue{{Field: "sms_body", Code: "too_long", Blocking: true}}}
	rec := serve(fakeCampaigns{approveEr: verr}, http.MethodPost, "/api/campaigns/1/approve", `{"version":1}`)
	var got struct {
		Issues []content.Issue `json:"issues"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != 422 || len(got.Issues) != 1 {
		t.Fatalf("validation: %d %s", rec.Code, rec.Body)
	}
}

func TestVariantErrors(t *testing.T) {
	for err, status := range map[error]int{content.ErrApproved: 409, content.ErrInvalid: 400, content.ErrNotFound: 404} {
		rec := serve(fakeCampaigns{approveEr: err}, http.MethodPut, "/api/campaigns/1/variants", `{"versions":[1,2]}`)
		if rec.Code != status {
			t.Errorf("%v: %d", err, rec.Code)
		}
	}
}
