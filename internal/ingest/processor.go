package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/inatews/inatews-cloud-run/internal/model"
)

type Result string

const (
	ResultApplied   Result = "applied"
	ResultDuplicate Result = "duplicate"
	ResultStale     Result = "stale"
)

type Processor interface {
	Process(context.Context, model.Envelope, string) (Result, error)
}

type SQLProcessor struct {
	db *sql.DB
}

func NewSQLProcessor(db *sql.DB) *SQLProcessor {
	return &SQLProcessor{db: db}
}

func (p *SQLProcessor) Process(ctx context.Context, envelope model.Envelope, pubsubMessageID string) (Result, error) {
	started := time.Now()
	if err := envelope.NormalizeAndValidate(); err != nil {
		return "", err
	}

	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return "", fmt.Errorf("begin ingest transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO sync_receipts
			(message_id, pubsub_message_id, entity_type, entity_key, operation, status)
		VALUES (?, ?, ?, ?, ?, 'processing')`,
		envelope.MessageID, nullableString(pubsubMessageID), envelope.EntityType,
		envelope.EntityKey, envelope.Operation,
	)
	if err != nil {
		if isDuplicateKey(err) {
			if _, updateErr := tx.ExecContext(ctx, `
				UPDATE sync_receipts
				SET delivery_count = delivery_count + 1, last_received_at = CURRENT_TIMESTAMP(6)
				WHERE message_id = ?`, envelope.MessageID); updateErr != nil {
				return "", fmt.Errorf("update duplicate receipt: %w", updateErr)
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return "", fmt.Errorf("commit duplicate receipt: %w", commitErr)
			}
			return ResultDuplicate, nil
		}
		return "", fmt.Errorf("insert sync receipt: %w", err)
	}

	// Insert a sentinel version row when this entity has never been observed.
	// The no-op duplicate clause acquires the existing row lock so concurrent
	// updates to one entity are serialized before comparing their versions.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO sync_entity_versions
			(entity_type, entity_key, source_updated_at, source_sequence, message_id, operation)
		VALUES (?, ?, '1970-01-01 00:00:00.000000', 0, '', 'upsert')
		ON DUPLICATE KEY UPDATE entity_key = VALUES(entity_key)`,
		envelope.EntityType, envelope.EntityKey,
	)
	if err != nil {
		return "", fmt.Errorf("lock entity version: %w", err)
	}

	var currentTime time.Time
	var currentSequence uint64
	err = tx.QueryRowContext(ctx, `
		SELECT source_updated_at, source_sequence
		FROM sync_entity_versions
		WHERE entity_type = ? AND entity_key = ?
		FOR UPDATE`, envelope.EntityType, envelope.EntityKey,
	).Scan(&currentTime, &currentSequence)
	if err != nil {
		return "", fmt.Errorf("read entity version: %w", err)
	}

	if isStale(envelope.SourceUpdatedAt, envelope.SourceSequence, currentTime, currentSequence) {
		_, err = tx.ExecContext(ctx, `
			UPDATE sync_receipts
			SET status = 'stale', reason = 'older source version', processed_at = CURRENT_TIMESTAMP(6),
			    processing_duration_ms = ?
			WHERE message_id = ?`, uint64(time.Since(started).Milliseconds()), envelope.MessageID)
		if err != nil {
			return "", fmt.Errorf("mark stale receipt: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("commit stale receipt: %w", err)
		}
		return ResultStale, nil
	}

	if err := storeReplica(ctx, tx, envelope); err != nil {
		return "", err
	}
	if envelope.EntityType == "earthquake" {
		if err := projectEarthquake(ctx, tx, envelope); err != nil {
			return "", err
		}
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE sync_entity_versions
		SET source_updated_at = ?, source_sequence = ?, message_id = ?, operation = ?
		WHERE entity_type = ? AND entity_key = ?`,
		envelope.SourceUpdatedAt, envelope.SourceSequence, envelope.MessageID,
		envelope.Operation, envelope.EntityType, envelope.EntityKey,
	)
	if err != nil {
		return "", fmt.Errorf("update entity version: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE sync_receipts
		SET status = 'applied', processed_at = CURRENT_TIMESTAMP(6), processing_duration_ms = ?
		WHERE message_id = ?`, uint64(time.Since(started).Milliseconds()), envelope.MessageID)
	if err != nil {
		return "", fmt.Errorf("complete sync receipt: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit ingest transaction: %w", err)
	}
	return ResultApplied, nil
}

func storeReplica(ctx context.Context, tx *sql.Tx, envelope model.Envelope) error {
	deleted := envelope.Operation == "delete"
	var payload any
	if !deleted {
		payload = []byte(envelope.Payload)
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO replicated_entities
			(entity_type, entity_key, event_id, source_row_id, source_updated_at,
			 source_sequence, payload, is_deleted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			event_id = VALUES(event_id),
			source_row_id = VALUES(source_row_id),
			source_updated_at = VALUES(source_updated_at),
			source_sequence = VALUES(source_sequence),
			payload = VALUES(payload),
			is_deleted = VALUES(is_deleted)`,
		envelope.EntityType, envelope.EntityKey, nullableString(envelope.EventID),
		nullableInt64(envelope.SourceRowID), envelope.SourceUpdatedAt,
		envelope.SourceSequence, payload, deleted,
	)
	if err != nil {
		return fmt.Errorf("store replicated entity: %w", err)
	}
	return nil
}

func projectEarthquake(ctx context.Context, tx *sql.Tx, envelope model.Envelope) error {
	if envelope.Operation == "delete" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM earthquake_archive_events WHERE eventid = ?`, envelope.EntityKey); err != nil {
			return fmt.Errorf("delete earthquake projection: %w", err)
		}
		return nil
	}

	var payload model.EarthquakePayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return fmt.Errorf("%w: decode earthquake payload: %v", model.ErrInvalidEnvelope, err)
	}
	if payload.EventID == "" {
		payload.EventID = envelope.EventID
	}
	if payload.EventID == "" {
		payload.EventID = envelope.EntityKey
	}
	if payload.EventID != envelope.EntityKey {
		return fmt.Errorf("%w: earthquake payload eventid does not match entity_key", model.ErrInvalidEnvelope)
	}

	var eventTime any
	if payload.DatetimeUTC != nil && *payload.DatetimeUTC != "" {
		parsed, err := time.Parse(time.RFC3339Nano, *payload.DatetimeUTC)
		if err != nil {
			return fmt.Errorf("%w: invalid earthquake datetime_utc: %v", model.ErrInvalidEnvelope, err)
		}
		eventTime = parsed.UTC()
	}

	properties := []byte("{}")
	if len(payload.Properties) > 0 && string(payload.Properties) != "null" {
		if !json.Valid(payload.Properties) {
			return fmt.Errorf("%w: earthquake properties must be valid JSON", model.ErrInvalidEnvelope)
		}
		properties = payload.Properties
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO earthquake_archive_events
			(eventid, source_row_id, source, mag, depth_km, latitude, longitude,
			 place, wib_date, wib_time, datetime_utc, type, status, properties,
			 has_moment_tensor, has_felt_data, has_damage_data, has_narasi,
			 has_m5_payload, has_eq_phase, source_updated_at, source_sequence)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			source_row_id = VALUES(source_row_id), source = VALUES(source),
			mag = VALUES(mag), depth_km = VALUES(depth_km),
			latitude = VALUES(latitude), longitude = VALUES(longitude),
			place = VALUES(place), wib_date = VALUES(wib_date), wib_time = VALUES(wib_time),
			datetime_utc = VALUES(datetime_utc), type = VALUES(type), status = VALUES(status),
			properties = VALUES(properties), has_moment_tensor = VALUES(has_moment_tensor),
			has_felt_data = VALUES(has_felt_data), has_damage_data = VALUES(has_damage_data),
			has_narasi = VALUES(has_narasi), has_m5_payload = VALUES(has_m5_payload),
			has_eq_phase = VALUES(has_eq_phase), source_updated_at = VALUES(source_updated_at),
			source_sequence = VALUES(source_sequence)`,
		payload.EventID, nullableInt64(envelope.SourceRowID), payload.Source,
		payload.Magnitude, payload.DepthKM, payload.Latitude, payload.Longitude,
		payload.Place, payload.WIBDate, payload.WIBTime, eventTime, payload.Type,
		payload.Status, properties, payload.HasMomentTensor, payload.HasFeltData,
		payload.HasDamageData, payload.HasNarasi, payload.HasM5Payload,
		payload.HasEqPhase, envelope.SourceUpdatedAt, envelope.SourceSequence,
	)
	if err != nil {
		return fmt.Errorf("upsert earthquake projection: %w", err)
	}
	return nil
}

func isStale(incomingTime time.Time, incomingSequence uint64, currentTime time.Time, currentSequence uint64) bool {
	if incomingTime.Before(currentTime) {
		return true
	}
	return incomingTime.Equal(currentTime) && incomingSequence < currentSequence
}

func isDuplicateKey(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}
