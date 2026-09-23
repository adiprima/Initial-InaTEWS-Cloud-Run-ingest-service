package publicapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrNotFound = errors.New("not found")

type Repository interface {
	ListEarthquakes(context.Context, ListFilter) (Page, error)
	GetEarthquake(context.Context, string) (EarthquakeDetail, error)
}

type SQLRepository struct {
	db *sql.DB
}

func NewSQLRepository(db *sql.DB) *SQLRepository {
	return &SQLRepository{db: db}
}

func (r *SQLRepository) ListEarthquakes(ctx context.Context, filter ListFilter) (Page, error) {
	query := `
		SELECT eventid, source_row_id, source, mag, depth_km, latitude, longitude,
		       place, wib_date, wib_time, datetime_utc, type, status, properties,
		       has_moment_tensor, has_felt_data, has_damage_data, has_narasi,
		       has_m5_payload, has_eq_phase, source_updated_at, source_sequence
		FROM earthquake_archive_events
		WHERE datetime_utc IS NOT NULL`
	args := make([]any, 0, 12)

	if filter.MinMag != nil {
		query += " AND mag >= ?"
		args = append(args, *filter.MinMag)
	}
	if filter.MaxMag != nil {
		query += " AND mag <= ?"
		args = append(args, *filter.MaxMag)
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}
	if filter.StartTime != nil {
		query += " AND datetime_utc >= ?"
		args = append(args, filter.StartTime.UTC())
	}
	if filter.EndTime != nil {
		query += " AND datetime_utc <= ?"
		args = append(args, filter.EndTime.UTC())
	}
	if filter.Cursor != "" {
		cursorTime, cursorEventID, err := decodeCursor(filter.Cursor)
		if err != nil {
			return Page{}, err
		}
		query += " AND (datetime_utc < ? OR (datetime_utc = ? AND eventid < ?))"
		args = append(args, cursorTime, cursorTime, cursorEventID)
	}

	query += " ORDER BY datetime_utc DESC, eventid DESC LIMIT ?"
	args = append(args, filter.Limit+1)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return Page{}, fmt.Errorf("query earthquakes: %w", err)
	}
	defer rows.Close()

	items := make([]Earthquake, 0, filter.Limit+1)
	for rows.Next() {
		item, err := scanEarthquake(rows)
		if err != nil {
			return Page{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("iterate earthquakes: %w", err)
	}

	page := Page{Items: items, HasMore: len(items) > filter.Limit}
	if page.HasMore {
		page.Items = items[:filter.Limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = encodeCursor(*last.DatetimeUTC, last.EventID)
	}
	return page, nil
}

func (r *SQLRepository) GetEarthquake(ctx context.Context, eventID string) (EarthquakeDetail, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT eventid, source_row_id, source, mag, depth_km, latitude, longitude,
		       place, wib_date, wib_time, datetime_utc, type, status, properties,
		       has_moment_tensor, has_felt_data, has_damage_data, has_narasi,
		       has_m5_payload, has_eq_phase, source_updated_at, source_sequence
		FROM earthquake_archive_events
		WHERE eventid = ?`, eventID)
	item, err := scanEarthquake(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EarthquakeDetail{}, ErrNotFound
	}
	if err != nil {
		return EarthquakeDetail{}, err
	}

	detail := EarthquakeDetail{
		Earthquake:    item,
		SourcePayload: json.RawMessage(`{}`),
		Related: map[string][]RelatedEntity{
			"tsunami": {}, "moment_tensor": {}, "felt": {}, "damage": {},
			"narasi": {}, "m5": {}, "eq_phase": {},
		},
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT entity_type, entity_key, source_row_id, source_updated_at,
		       source_sequence, payload
		FROM replicated_entities
		WHERE event_id = ? AND is_deleted = FALSE
		ORDER BY entity_type, source_updated_at, source_sequence`, eventID)
	if err != nil {
		return EarthquakeDetail{}, fmt.Errorf("query related earthquake data: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var entityType, entityKey string
		var sourceRowID sql.NullInt64
		var sourceUpdatedAt time.Time
		var sourceSequence uint64
		var payload []byte
		if err := rows.Scan(&entityType, &entityKey, &sourceRowID, &sourceUpdatedAt, &sourceSequence, &payload); err != nil {
			return EarthquakeDetail{}, fmt.Errorf("scan related earthquake data: %w", err)
		}
		if len(payload) == 0 || !json.Valid(payload) {
			payload = []byte("{}")
		}
		if entityType == "earthquake" {
			detail.SourcePayload = append(json.RawMessage(nil), payload...)
			continue
		}
		detail.Related[entityType] = append(detail.Related[entityType], RelatedEntity{
			EntityKey: entityKey, SourceRowID: int64Ptr(sourceRowID),
			SourceUpdatedAt: sourceUpdatedAt.UTC(), SourceSequence: sourceSequence,
			Payload: append(json.RawMessage(nil), payload...),
		})
	}
	if err := rows.Err(); err != nil {
		return EarthquakeDetail{}, fmt.Errorf("iterate related earthquake data: %w", err)
	}
	return detail, nil
}

type scanner interface {
	Scan(...any) error
}

func scanEarthquake(row scanner) (Earthquake, error) {
	var item Earthquake
	var sourceRowID sql.NullInt64
	var source, place, wibDate, wibTime, eventType, status sql.NullString
	var magnitude, depthKM, latitude, longitude sql.NullFloat64
	var datetimeUTC sql.NullTime
	var properties []byte
	if err := row.Scan(
		&item.EventID, &sourceRowID, &source, &magnitude, &depthKM, &latitude,
		&longitude, &place, &wibDate, &wibTime, &datetimeUTC, &eventType, &status,
		&properties, &item.HasMomentTensor, &item.HasFeltData, &item.HasDamageData,
		&item.HasNarrative, &item.HasM5Payload, &item.HasEQPhase,
		&item.SourceUpdatedAt, &item.SourceSequence,
	); err != nil {
		return Earthquake{}, err
	}
	item.SourceRowID = int64Ptr(sourceRowID)
	item.Source = stringPtr(source)
	item.Magnitude = float64Ptr(magnitude)
	item.DepthKM = float64Ptr(depthKM)
	item.Latitude = float64Ptr(latitude)
	item.Longitude = float64Ptr(longitude)
	item.Place = stringPtr(place)
	item.WIBDate = stringPtr(wibDate)
	item.WIBTime = stringPtr(wibTime)
	item.Type = stringPtr(eventType)
	item.Status = stringPtr(status)
	if datetimeUTC.Valid {
		value := datetimeUTC.Time.UTC()
		item.DatetimeUTC = &value
	}
	if len(properties) == 0 {
		properties = []byte("{}")
	}
	item.Properties = properties
	return item, nil
}

func encodeCursor(value time.Time, eventID string) string {
	raw := value.UTC().Format(time.RFC3339Nano) + "\n" + eventID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(value string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	parts := strings.SplitN(string(raw), "\n", 2)
	if len(parts) != 2 || parts[1] == "" {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	parsed, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("invalid cursor")
	}
	return parsed.UTC(), parts[1], nil
}

func int64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func float64Ptr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	return &value.Float64
}

func stringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}
