package publicapi

import (
	"context"
	"database/sql"
	"encoding/json"
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
	item EarthquakeDetail
	err  error
}

func (f *fakeRepository) ListEarthquakes(_ context.Context, _ ListFilter) (Page, error) {
	return f.page, f.err
}

func (f *fakeRepository) GetEarthquake(_ context.Context, _ string) (EarthquakeDetail, error) {
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

func TestOpenAPIAndSwaggerArePublic(t *testing.T) {
	server := newTestServer(&fakeRepository{}, fakeAuthorizer{err: ErrUnauthorized})

	openAPIRequest := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	openAPIResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(openAPIResponse, openAPIRequest)
	if openAPIResponse.Code != http.StatusOK || !json.Valid(openAPIResponse.Body.Bytes()) {
		t.Fatalf("openapi status = %d, valid JSON = %v", openAPIResponse.Code, json.Valid(openAPIResponse.Body.Bytes()))
	}
	if !strings.Contains(openAPIResponse.Body.String(), `"ApiKeyAuth"`) {
		t.Fatalf("OpenAPI document does not define API key security")
	}

	docsRequest := httptest.NewRequest(http.MethodGet, "/docs", nil)
	docsResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(docsResponse, docsRequest)
	if docsResponse.Code != http.StatusOK || !strings.Contains(docsResponse.Body.String(), "SwaggerUIBundle") {
		t.Fatalf("swagger status = %d, body = %s", docsResponse.Code, docsResponse.Body.String())
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

func TestDetailReturnsCompleteRelatedData(t *testing.T) {
	now := time.Date(2026, 9, 23, 1, 2, 3, 0, time.UTC)
	repository := &fakeRepository{item: EarthquakeDetail{
		Earthquake:    Earthquake{EventID: "evt-1", Properties: []byte("{}")},
		SourcePayload: []byte(`{"eventid":"evt-1","contributing_agencies":["BMKG"]}`),
		Related: map[string][]RelatedEntity{
			"moment_tensor": {{EntityKey: "moment_tensor:7", SourceUpdatedAt: now, Payload: []byte(`{"magnitude":5.6}`)}},
		},
	}}
	server := newTestServer(repository, fakeAuthorizer{})
	request := httptest.NewRequest(http.MethodGet, "/v1/earthquakes/evt-1", nil)
	request.Header.Set("X-API-Key", strings.Repeat("a", 32))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	for _, want := range []string{`"event_id":"evt-1"`, `"source_payload"`, `"moment_tensor"`, `"magnitude":5.6`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("body does not contain %s: %s", want, response.Body.String())
		}
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
