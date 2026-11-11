package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/smart-campaign-composer/backend/internal/customers"
)

type fakeStore struct {
	filter        customers.Filter
	limit, offset int
	category      string
	err           error
}

func (f *fakeStore) Audiences(_ context.Context, _ time.Time, category string) (customers.AudienceReport, error) {
	f.category = category
	return customers.AudienceReport{Audiences: []customers.Audience{}}, f.err
}

func (f *fakeStore) Summary(context.Context, time.Time) (customers.Summary, error) {
	return customers.Summary{Total: 3, Cities: []string{"Ankara"}}, f.err
}

func (f *fakeStore) List(_ context.Context, flt customers.Filter, _ time.Time, limit, offset int) ([]customers.Customer, int, error) {
	f.filter, f.limit, f.offset = flt, limit, offset
	return []customers.Customer{}, 0, f.err
}

func (f *fakeStore) Get(_ context.Context, id int64) (customers.CustomerDetail, error) {
	if f.err != nil {
		return customers.CustomerDetail{}, f.err
	}
	if id != 7 {
		return customers.CustomerDetail{}, customers.ErrNotFound
	}
	return customers.CustomerDetail{Customer: customers.Customer{ID: 7}, RecentOrders: []customers.Order{}}, nil
}

func TestListSort(t *testing.T) {
	s := &fakeStore{}
	if rec := getCustomers(t, s, "/api/customers?sort=total_spent&order=desc"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if s.filter.Sort != (customers.Sort{Field: "total_spent", Desc: true}) {
		t.Fatalf("sort = %+v", s.filter.Sort)
	}
	for _, q := range []string{"sort=id%3BDROP", "sort=email", "sort=name&order=up", "order=DESC"} {
		if rec := getCustomers(t, &fakeStore{}, "/api/customers?"+q); rec.Code != http.StatusBadRequest {
			t.Errorf("%q: status = %d, want 400", q, rec.Code)
		}
	}
}

func TestGetCustomer(t *testing.T) {
	for target, want := range map[string]int{
		"/api/customers/7":       http.StatusOK,
		"/api/customers/8":       http.StatusNotFound,
		"/api/customers/0":       http.StatusBadRequest,
		"/api/customers/abc":     http.StatusBadRequest,
		"/api/customers/summary": http.StatusOK, // literal route still wins over {id}
	} {
		if rec := getCustomers(t, &fakeStore{}, target); rec.Code != want {
			t.Errorf("%s: status = %d, want %d", target, rec.Code, want)
		}
	}
	rec := getCustomers(t, &fakeStore{err: errors.New("pq: secret")}, "/api/customers/7")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("store error: status=%d body=%q", rec.Code, rec.Body)
	}
}

func getCustomers(t *testing.T, s *fakeStore, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h := newHandler(slog.New(slog.DiscardHandler), fakeDB{}, s, nil, nil)
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestListParsesParameters(t *testing.T) {
	s := &fakeStore{}
	rec := getCustomers(t, s, "/api/customers?page=3&page_size=50&city=Istanbul&email_consent=true&sms_consent=false")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if s.limit != 50 || s.offset != 100 || s.filter.City != "Istanbul" ||
		s.filter.EmailConsent == nil || !*s.filter.EmailConsent ||
		s.filter.SMSConsent == nil || *s.filter.SMSConsent {
		t.Fatalf("unexpected store call: %+v limit=%d offset=%d", s.filter, s.limit, s.offset)
	}
	var body customerList
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Items == nil || body.Page != 3 {
		t.Fatalf("bad body %q (err %v)", rec.Body, err)
	}
}

func TestListDefaults(t *testing.T) {
	s := &fakeStore{}
	getCustomers(t, s, "/api/customers")
	if s.limit != defaultPerPage || s.offset != 0 || s.filter.EmailConsent != nil || s.filter.SMSConsent != nil {
		t.Fatalf("unexpected defaults: %+v limit=%d offset=%d", s.filter, s.limit, s.offset)
	}
}

func TestListRejectsInvalidInput(t *testing.T) {
	for _, q := range []string{
		"page=0", "page=-1", "page=abc", "page=99999999999",
		"page_size=0", "page_size=101", "page_size=x",
		"email_consent=yes", "sms_consent=1",
		"city=" + strings.Repeat("x", 65),
		"segment=drop_table", "segment=category_buyers", // unknown id; category missing
		"segment=" + strings.Repeat("x", 65),
	} {
		s := &fakeStore{}
		rec := getCustomers(t, s, "/api/customers?"+q)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%q: status = %d, want 400", q, rec.Code)
		}
		if s.limit != 0 {
			t.Errorf("%q: store was called despite invalid input", q)
		}
	}
}

func TestStoreErrorsAreHiddenFrom500(t *testing.T) {
	s := &fakeStore{err: errors.New("pq: secret connection detail")}
	for _, target := range []string{"/api/customers", "/api/customers/summary"} {
		rec := getCustomers(t, s, target)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status = %d", target, rec.Code)
		}
		if got := rec.Body.String(); got != "{\"error\":\"internal error\"}\n" {
			t.Fatalf("%s: body leaks detail: %q", target, got)
		}
	}
}

func TestSummary(t *testing.T) {
	rec := getCustomers(t, &fakeStore{}, "/api/customers/summary")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status = %d, content-type = %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestListSegmentParameters(t *testing.T) {
	s := &fakeStore{}
	rec := getCustomers(t, s, "/api/customers?segment=category_buyers&category=electronics")
	if rec.Code != http.StatusOK || s.filter.Segment != "category_buyers" || s.filter.Category != "electronics" {
		t.Fatalf("status=%d filter=%+v", rec.Code, s.filter)
	}
	if rec := getCustomers(t, &fakeStore{}, "/api/customers?segment=vip"); rec.Code != http.StatusOK {
		t.Fatalf("vip without category: status = %d", rec.Code)
	}
}

func TestAudiences(t *testing.T) {
	s := &fakeStore{}
	if rec := getCustomers(t, s, "/api/audiences?category=toys"); rec.Code != http.StatusOK || s.category != "toys" {
		t.Fatalf("status=%d category=%q", rec.Code, s.category)
	}
	if rec := getCustomers(t, s, "/api/audiences?category="+strings.Repeat("x", 65)); rec.Code != http.StatusBadRequest {
		t.Fatalf("long category: status = %d", rec.Code)
	}
	s.err = errors.New("boom")
	if rec := getCustomers(t, s, "/api/audiences"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("store error: status = %d", rec.Code)
	}
}
