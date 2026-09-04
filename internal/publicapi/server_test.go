package publicapi

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeRepository struct {
	page Page
	item Earthquake
	err  error
}

func (f *fakeRepository) ListEarthquakes(_ context.Context, _ ListFilter) (Page, error) {
	return f.page, f.err
}

func (f *fakeRepository) GetEarthquake(_ context.Context, _ string) (Earthquake, error) {
	return f.item, f.err
}

type fakeAuthorizer struct {
	err error
}

func (f fakeAuthorizer) Authorize(_ context.Context, _, _ string) (Principal, error) {
	return Principal{APIKeyID: 1, Privileges: map[string]bool{"earthquakes:read": true}, RateLimit: 60}, f.err
}

func TestListRequiresAPIKey(t *testing.T) {
	server := newTestServer(&fakeRepository{}, fakeAuthorizer{err: ErrUnauthorized})
	request := httptest.NewRequest(http.MethodGet, "/v1/earthquakes", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestListReturnsEarthquakes(t *testing.T) {
	now := time.Date(2026, 9, 4, 5, 0, 0, 0, time.UTC)
	repository := &fakeRepository{page: Page{Items: []Earthquake{{EventID: "test-1", DatetimeUTC: &now, Properties: []byte("{}")}}}}
	server := newTestServer(repository, fakeAuthorizer{})
	request := httptest.NewRequest(http.MethodGet, "/v1/earthquakes?limit=5", nil)
	request.Header.Set("X-API-Key", strings.Repeat("a", 32))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"event_id":"test-1"`) {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
}

func TestListRejectsInvalidLimit(t *testing.T) {
	server := newTestServer(&fakeRepository{}, fakeAuthorizer{})
	request := httptest.NewRequest(http.MethodGet, "/v1/earthquakes?limit=101", nil)
	request.Header.Set("X-API-Key", strings.Repeat("a", 32))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestCursorRoundTrip(t *testing.T) {
	wantTime := time.Date(2026, 9, 4, 5, 0, 0, 123456000, time.UTC)
	wantID := "event-123"
	gotTime, gotID, err := decodeCursor(encodeCursor(wantTime, wantID))
	if err != nil {
		t.Fatal(err)
	}
	if !gotTime.Equal(wantTime) || gotID != wantID {
		t.Fatalf("cursor = %s/%s, want %s/%s", gotTime, gotID, wantTime, wantID)
	}
}

func newTestServer(repository Repository, authorizer Authorizer) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(&sql.DB{}, repository, authorizer, logger, Config{DefaultPageSize: 20, MaxPageSize: 100, AllowAllOrigins: true})
}
