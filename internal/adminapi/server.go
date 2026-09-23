package adminapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var entityTypes = []string{"earthquake", "tsunami", "moment_tensor", "felt", "damage", "narasi", "m5", "eq_phase"}

type MetricsProvider interface {
	Snapshot(context.Context) map[string]any
}

type Server struct {
	db      *sql.DB
	logger  *slog.Logger
	metrics MetricsProvider
}

func NewServer(db *sql.DB, logger *slog.Logger, metrics MetricsProvider) *Server {
	return &Server{db: db, logger: logger, metrics: metrics}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /ready", s.ready)
	mux.HandleFunc("GET /v1/overview", s.overview)
	mux.HandleFunc("GET /v1/entities", s.entities)
	mux.HandleFunc("GET /v1/events/{eventid}", s.event)
	mux.HandleFunc("POST /v1/receipts/verify", s.verifyReceipts)
	mux.HandleFunc("GET /v1/api/metrics", s.apiMetrics)
	mux.HandleFunc("GET /v1/api-clients", s.listClients)
	mux.HandleFunc("POST /v1/api-clients", s.createClient)
	mux.HandleFunc("PATCH /v1/api-clients/{id}", s.updateClient)
	mux.HandleFunc("POST /v1/api-clients/{id}/keys", s.createKey)
	mux.HandleFunc("PATCH /v1/api-keys/{id}", s.updateKey)
	mux.HandleFunc("POST /v1/api-keys/{id}/rotate", s.rotateKey)
	mux.HandleFunc("POST /v1/api-keys/{id}/suspend", s.suspendKey)
	mux.HandleFunc("POST /v1/api-keys/{id}/activate", s.activateKey)
	mux.HandleFunc("POST /v1/api-keys/{id}/revoke", s.revokeKey)
	mux.HandleFunc("GET /v1/audit", s.audit)
	return s.recover(s.logging(mux))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		writeError(w, 503, "service_not_ready", "Cloud SQL is unavailable")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"generated_at": time.Now().UTC(), "entities": s.entityStats(r.Context())}
	var total, applied, stale, processing, deliveries uint64
	var latest sql.NullTime
	err := s.db.QueryRowContext(r.Context(), `
		SELECT COUNT(*), COALESCE(SUM(status='applied'),0), COALESCE(SUM(status='stale'),0),
		       COALESCE(SUM(status='processing'),0), COALESCE(SUM(delivery_count),0), MAX(last_received_at)
		FROM sync_receipts WHERE received_at >= UTC_TIMESTAMP() - INTERVAL 24 HOUR`).Scan(
		&total, &applied, &stale, &processing, &deliveries, &latest)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	data["receipts_24h"] = map[string]any{"total": total, "applied": applied, "stale": stale, "processing": processing, "deliveries": deliveries, "latest_at": nullTime(latest)}
	if s.metrics != nil {
		data["gcp"] = s.metrics.Snapshot(r.Context())
	} else {
		data["gcp"] = map[string]any{"available": false}
	}
	writeJSON(w, 200, data)
}

func (s *Server) entities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"data": s.entityStats(r.Context()), "generated_at": time.Now().UTC()})
}

func (s *Server) entityStats(ctx context.Context) []map[string]any {
	stats := make(map[string]map[string]any, len(entityTypes))
	for _, kind := range entityTypes {
		stats[kind] = map[string]any{"entity_type": kind, "active": uint64(0), "deleted": uint64(0), "max_sequence": uint64(0), "last_updated_at": nil}
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT entity_type, COALESCE(SUM(is_deleted=FALSE),0), COALESCE(SUM(is_deleted=TRUE),0),
		       COALESCE(MAX(source_sequence),0), MAX(source_updated_at)
		FROM replicated_entities GROUP BY entity_type`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var kind string
			var active, deleted, sequence uint64
			var latest sql.NullTime
			if rows.Scan(&kind, &active, &deleted, &sequence, &latest) == nil {
				stats[kind] = map[string]any{"entity_type": kind, "active": active, "deleted": deleted, "max_sequence": sequence, "last_updated_at": nullTime(latest)}
			}
		}
	}
	result := make([]map[string]any, 0, len(entityTypes))
	for _, kind := range entityTypes {
		result = append(result, stats[kind])
	}
	return result
}

func (s *Server) event(w http.ResponseWriter, r *http.Request) {
	eventID := strings.TrimSpace(r.PathValue("eventid"))
	if eventID == "" || len(eventID) > 191 {
		writeError(w, 400, "invalid_event_id", "Invalid event ID")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT e.entity_type, e.entity_key, e.source_row_id, e.source_updated_at, e.source_sequence,
		       e.is_deleted, e.payload, e.updated_at, r.status, r.reason, r.received_at, r.processed_at
		FROM replicated_entities e
		LEFT JOIN sync_receipts r ON r.message_id=CONCAT('outbox-', e.source_sequence)
		WHERE e.event_id=? ORDER BY e.entity_type, e.source_sequence`, eventID)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var kind, key string
		var rowID sql.NullInt64
		var sourceAt, updatedAt time.Time
		var sequence uint64
		var deleted bool
		var payload []byte
		var receiptStatus, receiptReason sql.NullString
		var receivedAt, processedAt sql.NullTime
		if err := rows.Scan(&kind, &key, &rowID, &sourceAt, &sequence, &deleted, &payload, &updatedAt, &receiptStatus, &receiptReason, &receivedAt, &processedAt); err != nil {
			writeError(w, 500, "database_error", err.Error())
			return
		}
		var body any
		if len(payload) > 0 {
			_ = json.Unmarshal(payload, &body)
		}
		items = append(items, map[string]any{"entity_type": kind, "entity_key": key, "source_row_id": nullInt(rowID), "source_updated_at": sourceAt.UTC(), "source_sequence": sequence, "is_deleted": deleted, "payload": body, "cloud_updated_at": updatedAt.UTC(), "receipt": map[string]any{"status": nullString(receiptStatus), "reason": nullString(receiptReason), "received_at": nullTime(receivedAt), "processed_at": nullTime(processedAt)}})
	}
	writeJSON(w, 200, map[string]any{"event_id": eventID, "data": items})
}

func (s *Server) verifyReceipts(w http.ResponseWriter, r *http.Request) {
	var input struct {
		MessageIDs []string `json:"message_ids"`
	}
	if err := decodeJSON(r, &input); err != nil || len(input.MessageIDs) < 1 || len(input.MessageIDs) > 500 {
		writeError(w, 400, "invalid_request", "message_ids must contain 1-500 values")
		return
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(input.MessageIDs)), ",")
	args := make([]any, len(input.MessageIDs))
	for i, value := range input.MessageIDs {
		args[i] = strings.TrimSpace(value)
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT message_id, pubsub_message_id, entity_type, entity_key, operation, status, delivery_count, reason, received_at, processed_at, processing_duration_ms FROM sync_receipts WHERE message_id IN (`+placeholders+`)`, args...)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer rows.Close()
	items := make(map[string]any)
	for rows.Next() {
		var mid, kind, key, op, status string
		var pubsub, reason sql.NullString
		var deliveries uint64
		var received time.Time
		var processed sql.NullTime
		var duration sql.NullInt64
		if rows.Scan(&mid, &pubsub, &kind, &key, &op, &status, &deliveries, &reason, &received, &processed, &duration) == nil {
			items[mid] = map[string]any{"message_id": mid, "pubsub_message_id": nullString(pubsub), "entity_type": kind, "entity_key": key, "operation": op, "status": status, "delivery_count": deliveries, "reason": nullString(reason), "received_at": received.UTC(), "processed_at": nullTime(processed), "processing_duration_ms": nullInt(duration)}
		}
	}
	writeJSON(w, 200, map[string]any{"data": items})
}

func (s *Server) apiMetrics(w http.ResponseWriter, r *http.Request) {
	windows := map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}
	window := r.URL.Query().Get("window")
	if window == "" {
		window = "24h"
	}
	duration, ok := windows[window]
	if !ok {
		writeError(w, 400, "invalid_window", "window must be 1h, 24h, 7d, or 30d")
		return
	}
	start := time.Now().UTC().Add(-duration)
	rows, err := s.db.QueryContext(r.Context(), `SELECT minute_bucket, SUM(request_count), SUM(CASE WHEN status_code>=400 AND status_code<500 THEN request_count ELSE 0 END), SUM(CASE WHEN status_code>=500 THEN request_count ELSE 0 END), SUM(CASE WHEN status_code=429 THEN request_count ELSE 0 END), SUM(total_duration_ms), MAX(max_duration_ms) FROM api_request_minutes WHERE minute_bucket>=? GROUP BY minute_bucket ORDER BY minute_bucket`, start)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer rows.Close()
	series := make([]map[string]any, 0)
	var total, clientErr, serverErr, limited, durationTotal, maxDuration uint64
	for rows.Next() {
		var bucket time.Time
		var count, c4, c5, rl, sum, max uint64
		if rows.Scan(&bucket, &count, &c4, &c5, &rl, &sum, &max) == nil {
			series = append(series, map[string]any{"time": bucket.UTC(), "requests": count, "client_errors": c4, "server_errors": c5, "rate_limited": rl, "avg_duration_ms": average(sum, count), "max_duration_ms": max})
			total += count
			clientErr += c4
			serverErr += c5
			limited += rl
			durationTotal += sum
			if max > maxDuration {
				maxDuration = max
			}
		}
	}
	routeRows, _ := s.db.QueryContext(r.Context(), `SELECT method,route,SUM(request_count),SUM(CASE WHEN status_code>=400 AND status_code<500 THEN request_count ELSE 0 END),SUM(CASE WHEN status_code>=500 THEN request_count ELSE 0 END),SUM(total_duration_ms),MAX(max_duration_ms) FROM api_request_minutes WHERE minute_bucket>=? GROUP BY method,route ORDER BY SUM(request_count) DESC`, start)
	routes := make([]map[string]any, 0)
	if routeRows != nil {
		defer routeRows.Close()
		for routeRows.Next() {
			var method, route string
			var count, c4, c5, sum, max uint64
			if routeRows.Scan(&method, &route, &count, &c4, &c5, &sum, &max) == nil {
				routes = append(routes, map[string]any{"method": method, "route": route, "requests": count, "client_errors": c4, "server_errors": c5, "avg_duration_ms": average(sum, count), "max_duration_ms": max})
			}
		}
	}
	keyRows, _ := s.db.QueryContext(r.Context(), `SELECT m.api_key_id, COALESCE(k.name,'anonymous/invalid'), COALESCE(k.key_prefix,''), SUM(m.request_count), SUM(m.total_duration_ms), MAX(k.last_used_at) FROM api_request_minutes m LEFT JOIN api_keys k ON k.id=m.api_key_id WHERE m.minute_bucket>=? GROUP BY m.api_key_id,k.name,k.key_prefix ORDER BY SUM(m.request_count) DESC LIMIT 50`, start)
	keys := make([]map[string]any, 0)
	if keyRows != nil {
		defer keyRows.Close()
		for keyRows.Next() {
			var id, count, sum uint64
			var name, prefix string
			var last sql.NullTime
			if keyRows.Scan(&id, &name, &prefix, &count, &sum, &last) == nil {
				keys = append(keys, map[string]any{"api_key_id": id, "name": name, "prefix": prefix, "requests": count, "avg_duration_ms": average(sum, count), "last_used_at": nullTime(last)})
			}
		}
	}
	writeJSON(w, 200, map[string]any{"window": window, "summary": map[string]any{"requests": total, "client_errors": clientErr, "server_errors": serverErr, "rate_limited": limited, "avg_duration_ms": average(durationTotal, total), "max_duration_ms": maxDuration}, "series": series, "routes": routes, "keys": keys})
}

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT c.id,c.name,c.status,c.default_rate_limit_per_minute,c.created_at,k.id,k.name,k.key_prefix,k.privileges,k.rate_limit_per_minute,k.expires_at,k.last_used_at,k.suspended_at,k.revoked_at,k.created_at FROM api_clients c LEFT JOIN api_keys k ON k.client_id=c.id ORDER BY c.id,k.id`)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer rows.Close()
	ordered := make([]map[string]any, 0)
	indexes := map[uint64]int{}
	for rows.Next() {
		var cid, defaultLimit uint64
		var cname, cstatus string
		var ccreated time.Time
		var kid sql.NullInt64
		var kname, prefix sql.NullString
		var privileges []byte
		var limit sql.NullInt64
		var expires, last, suspended, revoked, kcreated sql.NullTime
		if err := rows.Scan(&cid, &cname, &cstatus, &defaultLimit, &ccreated, &kid, &kname, &prefix, &privileges, &limit, &expires, &last, &suspended, &revoked, &kcreated); err != nil {
			continue
		}
		idx, ok := indexes[cid]
		if !ok {
			idx = len(ordered)
			indexes[cid] = idx
			ordered = append(ordered, map[string]any{"id": cid, "name": cname, "status": cstatus, "default_rate_limit_per_minute": defaultLimit, "created_at": ccreated.UTC(), "keys": []map[string]any{}})
		}
		if kid.Valid {
			var priv any
			_ = json.Unmarshal(privileges, &priv)
			keys := ordered[idx]["keys"].([]map[string]any)
			ordered[idx]["keys"] = append(keys, map[string]any{"id": kid.Int64, "name": kname.String, "key_prefix": prefix.String, "privileges": priv, "rate_limit_per_minute": nullInt(limit), "expires_at": nullTime(expires), "last_used_at": nullTime(last), "suspended_at": nullTime(suspended), "revoked_at": nullTime(revoked), "created_at": nullTime(kcreated)})
		}
	}
	writeJSON(w, 200, map[string]any{"data": ordered})
}

type clientInput struct {
	Name             string `json:"name"`
	Status           string `json:"status"`
	DefaultRateLimit uint64 `json:"default_rate_limit_per_minute"`
}

func (s *Server) createClient(w http.ResponseWriter, r *http.Request) {
	var in clientInput
	if decodeJSON(r, &in) != nil || strings.TrimSpace(in.Name) == "" {
		writeError(w, 400, "invalid_request", "name is required")
		return
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if !validStatus(in.Status) {
		writeError(w, 400, "invalid_request", "invalid client status")
		return
	}
	if in.DefaultRateLimit == 0 {
		in.DefaultRateLimit = 60
	}
	if in.DefaultRateLimit > 100000 {
		writeError(w, 400, "invalid_request", "invalid rate limit")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(), `INSERT INTO api_clients(name,status,default_rate_limit_per_minute) VALUES(?,?,?)`, strings.TrimSpace(in.Name), in.Status, in.DefaultRateLimit)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	id, _ := res.LastInsertId()
	if err = s.writeAuditWith(r.Context(), tx, r, "client.create", "api_client", strconv.FormatInt(id, 10), map[string]any{"name": in.Name, "status": in.Status}); err != nil || tx.Commit() != nil {
		writeError(w, 500, "database_error", "unable to commit client and audit")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}
func (s *Server) updateClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, 400, "invalid_id", "invalid client id")
		return
	}
	var in clientInput
	if decodeJSON(r, &in) != nil || strings.TrimSpace(in.Name) == "" || !validStatus(in.Status) || in.DefaultRateLimit < 1 || in.DefaultRateLimit > 100000 {
		writeError(w, 400, "invalid_request", "name, status, and rate limit are required")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE api_clients SET name=?,status=?,default_rate_limit_per_minute=? WHERE id=?`, strings.TrimSpace(in.Name), in.Status, in.DefaultRateLimit, id)
	}
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer tx.Rollback()
	if err = s.writeAuditWith(r.Context(), tx, r, "client.update", "api_client", strconv.FormatUint(id, 10), map[string]any{"name": in.Name, "status": in.Status}); err != nil || tx.Commit() != nil {
		writeError(w, 500, "database_error", "unable to commit client and audit")
		return
	}
	writeJSON(w, 200, map[string]bool{"success": true})
}

type keyInput struct {
	Name       string     `json:"name"`
	Privileges []string   `json:"privileges"`
	RateLimit  *uint64    `json:"rate_limit_per_minute"`
	ExpiresAt  *time.Time `json:"expires_at"`
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	cid, ok := pathID(r, "id")
	if !ok {
		writeError(w, 400, "invalid_id", "invalid client id")
		return
	}
	var in keyInput
	if decodeJSON(r, &in) != nil {
		writeError(w, 400, "invalid_request", "invalid JSON")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer tx.Rollback()
	raw, id, err := s.insertKey(r.Context(), tx, cid, in)
	if err != nil {
		writeError(w, 400, "key_create_failed", err.Error())
		return
	}
	if err = s.writeAuditWith(r.Context(), tx, r, "key.create", "api_key", strconv.FormatUint(id, 10), map[string]any{"client_id": cid, "name": in.Name, "privileges": normalizePrivileges(in.Privileges)}); err != nil || tx.Commit() != nil {
		writeError(w, 500, "database_error", "unable to commit key and audit")
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "api_key": raw, "shown_once": true})
}
func (s *Server) updateKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, 400, "invalid_id", "invalid key id")
		return
	}
	var in keyInput
	if decodeJSON(r, &in) != nil || strings.TrimSpace(in.Name) == "" {
		writeError(w, 400, "invalid_request", "name is required")
		return
	}
	if in.RateLimit != nil && (*in.RateLimit < 1 || *in.RateLimit > 100000) {
		writeError(w, 400, "invalid_request", "rate_limit_per_minute must be between 1 and 100000")
		return
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now().UTC()) {
		writeError(w, 400, "invalid_request", "expires_at must be in the future")
		return
	}
	priv, _ := json.Marshal(normalizePrivileges(in.Privileges))
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE api_keys SET name=?,privileges=?,rate_limit_per_minute=?,expires_at=? WHERE id=?`, strings.TrimSpace(in.Name), priv, in.RateLimit, in.ExpiresAt, id)
	}
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer tx.Rollback()
	if err = s.writeAuditWith(r.Context(), tx, r, "key.update", "api_key", strconv.FormatUint(id, 10), map[string]any{"name": in.Name, "privileges": normalizePrivileges(in.Privileges)}); err != nil || tx.Commit() != nil {
		writeError(w, 500, "database_error", "unable to commit key and audit")
		return
	}
	writeJSON(w, 200, map[string]bool{"success": true})
}
func (s *Server) rotateKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, 400, "invalid_id", "invalid key id")
		return
	}
	var cid uint64
	var name string
	var privileges []byte
	var limit sql.NullInt64
	var expires sql.NullTime
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(r.Context(), `SELECT client_id,name,privileges,rate_limit_per_minute,expires_at FROM api_keys WHERE id=? AND revoked_at IS NULL FOR UPDATE`, id).Scan(&cid, &name, &privileges, &limit, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, "not_found", "API key not found")
		return
	}
	var list []string
	_ = json.Unmarshal(privileges, &list)
	rate := (*uint64)(nil)
	if limit.Valid {
		v := uint64(limit.Int64)
		rate = &v
	}
	raw, newID, err := s.insertKey(r.Context(), tx, cid, keyInput{Name: name + " (rotated)", Privileges: list, RateLimit: rate, ExpiresAt: nil})
	if err != nil {
		writeError(w, 500, "rotate_failed", err.Error())
		return
	}
	grace := time.Now().UTC().Add(24 * time.Hour)
	if _, err = tx.ExecContext(r.Context(), `UPDATE api_keys SET expires_at=LEAST(COALESCE(expires_at,?),?) WHERE id=?`, grace, grace, id); err != nil {
		writeError(w, 500, "rotate_failed", err.Error())
		return
	}
	if err = s.writeAuditWith(r.Context(), tx, r, "key.rotate", "api_key", strconv.FormatUint(id, 10), map[string]any{"new_key_id": newID, "old_key_expires_at": grace}); err != nil || tx.Commit() != nil {
		writeError(w, 500, "database_error", "unable to commit rotation and audit")
		return
	}
	writeJSON(w, 201, map[string]any{"id": newID, "api_key": raw, "shown_once": true, "old_key_expires_at": grace})
}
func (s *Server) revokeKey(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, 400, "invalid_id", "invalid key id")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `UPDATE api_keys SET revoked_at=CURRENT_TIMESTAMP(6) WHERE id=? AND revoked_at IS NULL`, id)
	}
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer tx.Rollback()
	if err = s.writeAuditWith(r.Context(), tx, r, "key.revoke", "api_key", strconv.FormatUint(id, 10), map[string]any{}); err != nil || tx.Commit() != nil {
		writeError(w, 500, "database_error", "unable to commit revocation and audit")
		return
	}
	writeJSON(w, 200, map[string]bool{"success": true})
}

func (s *Server) suspendKey(w http.ResponseWriter, r *http.Request) {
	s.setKeySuspension(w, r, true)
}

func (s *Server) activateKey(w http.ResponseWriter, r *http.Request) {
	s.setKeySuspension(w, r, false)
}

func (s *Server) setKeySuspension(w http.ResponseWriter, r *http.Request, suspended bool) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, 400, "invalid_id", "invalid key id")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer tx.Rollback()
	var result sql.Result
	if suspended {
		result, err = tx.ExecContext(r.Context(), `UPDATE api_keys SET suspended_at=CURRENT_TIMESTAMP(6) WHERE id=? AND revoked_at IS NULL`, id)
	} else {
		result, err = tx.ExecContext(r.Context(), `UPDATE api_keys SET suspended_at=NULL WHERE id=? AND revoked_at IS NULL`, id)
	}
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		writeError(w, 404, "not_found", "active API key not found")
		return
	}
	action := "key.activate"
	if suspended {
		action = "key.suspend"
	}
	if err = s.writeAuditWith(r.Context(), tx, r, action, "api_key", strconv.FormatUint(id, 10), map[string]any{}); err != nil || tx.Commit() != nil {
		writeError(w, 500, "database_error", "unable to commit key status and audit")
		return
	}
	writeJSON(w, 200, map[string]bool{"success": true})
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s *Server) insertKey(ctx context.Context, executor sqlExecutor, clientID uint64, in keyInput) (string, uint64, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return "", 0, errors.New("name is required")
	}
	if in.RateLimit != nil && (*in.RateLimit < 1 || *in.RateLimit > 100000) {
		return "", 0, errors.New("rate_limit_per_minute must be between 1 and 100000")
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now().UTC()) {
		return "", 0, errors.New("expires_at must be in the future")
	}
	rawBytes := make([]byte, 24)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", 0, err
	}
	raw := "inatews_live_" + hex.EncodeToString(rawBytes)
	hash := sha256.Sum256([]byte(raw))
	prefix := raw
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	priv, _ := json.Marshal(normalizePrivileges(in.Privileges))
	res, err := executor.ExecContext(ctx, `INSERT INTO api_keys(client_id,key_prefix,key_hash,name,privileges,rate_limit_per_minute,expires_at) VALUES(?,?,?,?,?,?,?)`, clientID, prefix, hash[:], in.Name, priv, in.RateLimit, in.ExpiresAt)
	if err != nil {
		return "", 0, err
	}
	id, _ := res.LastInsertId()
	return raw, uint64(id), nil
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v, _ := strconv.Atoi(r.URL.Query().Get("limit")); v > 0 && v <= 200 {
		limit = v
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,actor_id,actor_email,action,target_type,target_id,metadata,created_at FROM admin_audit_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		writeError(w, 500, "database_error", err.Error())
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0)
	for rows.Next() {
		var id uint64
		var actorID, actorEmail, targetID sql.NullString
		var action, targetType string
		var metadata []byte
		var created time.Time
		if rows.Scan(&id, &actorID, &actorEmail, &action, &targetType, &targetID, &metadata, &created) == nil {
			var meta any
			_ = json.Unmarshal(metadata, &meta)
			items = append(items, map[string]any{"id": id, "actor_id": nullString(actorID), "actor_email": nullString(actorEmail), "action": action, "target_type": targetType, "target_id": nullString(targetID), "metadata": meta, "created_at": created.UTC()})
		}
	}
	writeJSON(w, 200, map[string]any{"data": items})
}
func (s *Server) writeAudit(ctx context.Context, r *http.Request, action, targetType, targetID string, metadata map[string]any) error {
	return s.writeAuditWith(ctx, s.db, r, action, targetType, targetID, metadata)
}
func (s *Server) writeAuditWith(ctx context.Context, executor sqlExecutor, r *http.Request, action, targetType, targetID string, metadata map[string]any) error {
	body, _ := json.Marshal(metadata)
	_, err := executor.ExecContext(ctx, `INSERT INTO admin_audit_logs(actor_id,actor_email,action,target_type,target_id,metadata) VALUES(?,?,?,?,?,?)`, strings.TrimSpace(r.Header.Get("X-InaTEWS-Admin-ID")), strings.TrimSpace(r.Header.Get("X-InaTEWS-Admin-Email")), action, targetType, targetID, body)
	return err
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) { sw.status = code; sw.ResponseWriter.WriteHeader(code) }
func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		s.logger.Info("admin API request", "method", r.Method, "path", r.URL.Path, "status", sw.status, "duration_ms", time.Since(started).Milliseconds())
	})
}
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if value := recover(); value != nil {
				s.logger.Error("admin API panic", "error", value)
				writeError(w, 500, "internal_error", "Internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func pathID(r *http.Request, name string) (uint64, bool) {
	id, err := strconv.ParseUint(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}
func nullTime(value sql.NullTime) any {
	if !value.Valid {
		return nil
	}
	return value.Time.UTC()
}
func nullInt(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}
func nullString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}
func average(sum, count uint64) float64 {
	if count == 0 {
		return 0
	}
	return float64(sum) / float64(count)
}
func validStatus(value string) bool {
	return value == "active" || value == "suspended" || value == "revoked"
}
func normalizePrivileges(values []string) []string {
	allowed := map[string]bool{"earthquakes:read": true}
	seen := map[string]bool{}
	result := make([]string, 0)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if allowed[value] && !seen[value] {
			result = append(result, value)
			seen[value] = true
		}
	}
	if len(result) == 0 {
		return []string{"earthquakes:read"}
	}
	return result
}
