package model

import (
	"errors"
	"testing"
	"time"
)

func TestEnvelopeNormalizesEntityKey(t *testing.T) {
	envelope := Envelope{
		SchemaVersion:   1,
		MessageID:       "message-1",
		EntityType:      " Earthquake ",
		Operation:       " UPSERT ",
		EventID:         "event-1",
		SourceUpdatedAt: time.Date(2026, 9, 3, 10, 0, 0, 0, time.FixedZone("WIB", 7*60*60)),
		Payload:         []byte(`{"eventid":"event-1"}`),
	}

	if err := envelope.NormalizeAndValidate(); err != nil {
		t.Fatalf("NormalizeAndValidate() error = %v", err)
	}
	if envelope.EntityKey != "event-1" {
		t.Fatalf("EntityKey = %q, want event-1", envelope.EntityKey)
	}
	if envelope.EntityType != "earthquake" || envelope.Operation != "upsert" {
		t.Fatalf("normalization failed: %#v", envelope)
	}
	if envelope.SourceUpdatedAt.Location() != time.UTC {
		t.Fatalf("SourceUpdatedAt location = %v, want UTC", envelope.SourceUpdatedAt.Location())
	}
}

func TestEnvelopeRejectsUnsupportedEntity(t *testing.T) {
	envelope := Envelope{
		SchemaVersion:   1,
		MessageID:       "message-1",
		EntityType:      "unknown",
		Operation:       "upsert",
		EntityKey:       "key",
		SourceUpdatedAt: time.Now(),
		Payload:         []byte(`{}`),
	}

	err := envelope.NormalizeAndValidate()
	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("error = %v, want ErrInvalidEnvelope", err)
	}
}

func TestEnvelopeAllowsDeleteWithoutPayload(t *testing.T) {
	envelope := Envelope{
		SchemaVersion:   1,
		MessageID:       "message-1",
		EntityType:      "tsunami",
		Operation:       "delete",
		EntityKey:       "event-1",
		SourceUpdatedAt: time.Now(),
	}

	if err := envelope.NormalizeAndValidate(); err != nil {
		t.Fatalf("NormalizeAndValidate() error = %v", err)
	}
}
