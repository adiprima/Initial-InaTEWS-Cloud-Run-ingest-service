package publicapi

import (
	"encoding/json"
	"time"
)

type Earthquake struct {
	EventID         string          `json:"event_id"`
	SourceRowID     *int64          `json:"source_row_id,omitempty"`
	Source          *string         `json:"source,omitempty"`
	Magnitude       *float64        `json:"magnitude,omitempty"`
	DepthKM         *float64        `json:"depth_km,omitempty"`
	Latitude        *float64        `json:"latitude,omitempty"`
	Longitude       *float64        `json:"longitude,omitempty"`
	Place           *string         `json:"place,omitempty"`
	WIBDate         *string         `json:"wib_date,omitempty"`
	WIBTime         *string         `json:"wib_time,omitempty"`
	DatetimeUTC     *time.Time      `json:"datetime_utc,omitempty"`
	Type            *string         `json:"type,omitempty"`
	Status          *string         `json:"status,omitempty"`
	Properties      json.RawMessage `json:"properties"`
	HasMomentTensor bool            `json:"has_moment_tensor"`
	HasFeltData     bool            `json:"has_felt_data"`
	HasDamageData   bool            `json:"has_damage_data"`
	HasNarrative    bool            `json:"has_narasi"`
	HasM5Payload    bool            `json:"has_m5_payload"`
	HasEQPhase      bool            `json:"has_eq_phase"`
	SourceUpdatedAt time.Time       `json:"source_updated_at"`
	SourceSequence  uint64          `json:"source_sequence"`
}

// EarthquakeDetail keeps the summary fields backward compatible while adding
// the complete source payload and every related entity replicated from InaTEWS.
type EarthquakeDetail struct {
	Earthquake
	SourcePayload json.RawMessage            `json:"source_payload"`
	Related       map[string][]RelatedEntity `json:"related"`
}

type RelatedEntity struct {
	EntityKey       string          `json:"entity_key"`
	SourceRowID     *int64          `json:"source_row_id,omitempty"`
	SourceUpdatedAt time.Time       `json:"source_updated_at"`
	SourceSequence  uint64          `json:"source_sequence"`
	Payload         json.RawMessage `json:"payload"`
}

type ListFilter struct {
	Limit     int
	Cursor    string
	MinMag    *float64
	MaxMag    *float64
	Status    string
	StartTime *time.Time
	EndTime   *time.Time
}

type Page struct {
	Items      []Earthquake `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
	HasMore    bool         `json:"has_more"`
}

type Principal struct {
	APIKeyID   uint64
	ClientID   uint64
	ClientName string
	Privileges map[string]bool
	RateLimit  uint64
}
