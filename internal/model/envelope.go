package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInvalidEnvelope = errors.New("invalid sync envelope")

var allowedEntityTypes = map[string]struct{}{
	"earthquake":    {},
	"tsunami":       {},
	"moment_tensor": {},
	"felt":          {},
	"damage":        {},
	"narasi":        {},
	"m5":            {},
	"eq_phase":      {},
}

type Envelope struct {
	SchemaVersion   int             `json:"schema_version"`
	MessageID       string          `json:"message_id"`
	EntityType      string          `json:"entity_type"`
	Operation       string          `json:"operation"`
	SourceRowID     int64           `json:"source_row_id,omitempty"`
	SourceSequence  uint64          `json:"source_sequence,omitempty"`
	EventID         string          `json:"event_id,omitempty"`
	EntityKey       string          `json:"entity_key,omitempty"`
	SourceUpdatedAt time.Time       `json:"source_updated_at"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

func (e *Envelope) NormalizeAndValidate() error {
	e.MessageID = strings.TrimSpace(e.MessageID)
	e.EntityType = strings.TrimSpace(strings.ToLower(e.EntityType))
	e.Operation = strings.TrimSpace(strings.ToLower(e.Operation))
	e.EventID = strings.TrimSpace(e.EventID)
	e.EntityKey = strings.TrimSpace(e.EntityKey)

	if e.SchemaVersion != 1 {
		return fmt.Errorf("%w: unsupported schema_version %d", ErrInvalidEnvelope, e.SchemaVersion)
	}
	if e.MessageID == "" || len(e.MessageID) > 191 {
		return fmt.Errorf("%w: message_id is required and must not exceed 191 bytes", ErrInvalidEnvelope)
	}
	if _, ok := allowedEntityTypes[e.EntityType]; !ok {
		return fmt.Errorf("%w: unsupported entity_type %q", ErrInvalidEnvelope, e.EntityType)
	}
	if e.Operation != "upsert" && e.Operation != "delete" {
		return fmt.Errorf("%w: operation must be upsert or delete", ErrInvalidEnvelope)
	}
	if e.EntityKey == "" {
		e.EntityKey = e.EventID
	}
	if e.EntityKey == "" || len(e.EntityKey) > 191 {
		return fmt.Errorf("%w: entity_key or event_id is required and must not exceed 191 bytes", ErrInvalidEnvelope)
	}
	if e.SourceUpdatedAt.IsZero() {
		return fmt.Errorf("%w: source_updated_at is required", ErrInvalidEnvelope)
	}
	if e.Operation == "upsert" && (len(e.Payload) == 0 || bytes.Equal(bytes.TrimSpace(e.Payload), []byte("null"))) {
		return fmt.Errorf("%w: payload is required for upsert", ErrInvalidEnvelope)
	}
	if len(e.Payload) > 0 && !json.Valid(e.Payload) {
		return fmt.Errorf("%w: payload must be valid JSON", ErrInvalidEnvelope)
	}

	e.SourceUpdatedAt = e.SourceUpdatedAt.UTC()
	return nil
}

type EarthquakePayload struct {
	EventID         string          `json:"eventid"`
	Magnitude       *float64        `json:"mag"`
	DepthKM         *float64        `json:"depth_km"`
	Latitude        *float64        `json:"latitude"`
	Longitude       *float64        `json:"longitude"`
	Place           *string         `json:"place"`
	DatetimeUTC     *string         `json:"datetime_utc"`
	Type            *string         `json:"type"`
	Status          *string         `json:"status"`
	Source          *string         `json:"source"`
	WIBDate         *string         `json:"wib_date"`
	WIBTime         *string         `json:"wib_time"`
	Properties      json.RawMessage `json:"properties"`
	HasMomentTensor Boolean         `json:"has_moment_tensor"`
	HasFeltData     Boolean         `json:"has_felt_data"`
	HasDamageData   Boolean         `json:"has_damage_data"`
	HasNarasi       Boolean         `json:"has_narasi"`
	HasM5Payload    Boolean         `json:"has_m5_payload"`
	HasEqPhase      Boolean         `json:"has_eq_phase"`
}

// Boolean accepts both JSON booleans and MySQL JSON_OBJECT boolean flags,
// which are encoded as the numbers 0 and 1 when sourced from TINYINT columns.
type Boolean bool

func (b *Boolean) UnmarshalJSON(data []byte) error {
	switch string(bytes.TrimSpace(data)) {
	case "true", "1":
		*b = true
		return nil
	case "false", "0", "null":
		*b = false
		return nil
	default:
		return fmt.Errorf("boolean must be true, false, 1, or 0")
	}
}

func (b Boolean) Bool() bool {
	return bool(b)
}
