package httpapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/inatews/inatews-cloud-run/internal/ingest"
	"github.com/inatews/inatews-cloud-run/internal/model"
)

type fakeProcessor struct {
	result   ingest.Result
	err      error
	received model.Envelope
}

func (f *fakeProcessor) Process(_ context.Context, envelope model.Envelope, _ string) (ingest.Result, error) {
	f.received = envelope
	return f.result, f.err
}

func TestPubSubPushAccepted(t *testing.T) {
	payload := `{"schema_version":1,"message_id":"outbox-1","entity_type":"earthquake","operation":"upsert","event_id":"event-1","source_updated_at":"2026-09-03T10:00:00Z","payload":{"eventid":"event-1"}}`
	body := fmt.Sprintf(`{"message":{"data":"%s","messageId":"pubsub-1"},"subscription":"projects/test/subscriptions/test"}`,
		base64.StdEncoding.EncodeToString([]byte(payload)))
	processor := &fakeProcessor{result: ingest.ResultApplied}
	server := newTestServer(processor)

	request := httptest.NewRequest(http.MethodPost, "/pubsub/push", strings.NewReader(body))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if processor.received.EntityKey != "event-1" {
		t.Fatalf("received EntityKey = %q", processor.received.EntityKey)
	}
}

func TestPubSubPushRejectsInvalidBase64(t *testing.T) {
	server := newTestServer(&fakeProcessor{})
	request := httptest.NewRequest(http.MethodPost, "/pubsub/push", strings.NewReader(
		`{"message":{"data":"%%%","messageId":"pubsub-1"}}`,
	))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}

func newTestServer(processor ingest.Processor) *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(&sql.DB{}, processor, logger, 10*1024*1024)
}
