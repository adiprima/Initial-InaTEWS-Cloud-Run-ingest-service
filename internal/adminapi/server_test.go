package adminapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

type capturedExecutor struct {
	query string
	args  []any
}

func (e *capturedExecutor) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	e.query, e.args = query, args
	return fixedResult(91), nil
}

type fixedResult int64

func (r fixedResult) LastInsertId() (int64, error) { return int64(r), nil }
func (r fixedResult) RowsAffected() (int64, error) { return 1, nil }

func TestInsertKeyStoresHashAndReturnsRawOnce(t *testing.T) {
	server := NewServer(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	executor := &capturedExecutor{}
	raw, id, err := server.insertKey(context.Background(), executor, 7, keyInput{Name: "mobile", Privileges: []string{"earthquakes:read"}})
	if err != nil {
		t.Fatal(err)
	}
	if id != 91 || !strings.HasPrefix(raw, "inatews_live_") || len(raw) != 61 {
		t.Fatalf("unexpected generated key response: id=%d key=%q", id, raw)
	}
	hash := sha256.Sum256([]byte(raw))
	storedHash, ok := executor.args[2].([]byte)
	if !ok || string(storedHash) != string(hash[:]) {
		t.Fatal("database argument does not contain the SHA-256 key hash")
	}
	for i, arg := range executor.args {
		if i != 1 && strings.Contains(stringify(arg), raw) {
			t.Fatal("raw API key was passed to persistent storage")
		}
	}
}

func TestRotateKeyUses24HourGraceAndAuditsWithoutRawKey(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := NewServer(db, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT client_id,name,privileges,rate_limit_per_minute,expires_at.*FOR UPDATE`).WithArgs(uint64(12)).
		WillReturnRows(sqlmock.NewRows([]string{"client_id", "name", "privileges", "rate_limit_per_minute", "expires_at"}).AddRow(4, "primary", `["earthquakes:read"]`, nil, nil))
	mock.ExpectExec(`INSERT INTO api_keys`).WithArgs(uint64(4), sqlmock.AnyArg(), sqlmock.AnyArg(), "primary (rotated)", sqlmock.AnyArg(), nil, nil).
		WillReturnResult(sqlmock.NewResult(13, 1))
	mock.ExpectExec(`UPDATE api_keys SET expires_at`).WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), uint64(12)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO admin_audit_logs`).WithArgs("1", "admin@example.test", "key.rotate", "api_key", "12", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	req := httptest.NewRequest("POST", "/v1/api-keys/12/rotate", nil)
	req.SetPathValue("id", "12")
	req.Header.Set("X-InaTEWS-Admin-ID", "1")
	req.Header.Set("X-InaTEWS-Admin-Email", "admin@example.test")
	recorder := httptest.NewRecorder()
	started := time.Now().UTC()
	server.rotateKey(recorder, req)
	if recorder.Code != 201 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		APIKey          string    `json:"api_key"`
		OldKeyExpiresAt time.Time `json:"old_key_expires_at"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.APIKey == "" || body.OldKeyExpiresAt.Before(started.Add(23*time.Hour+59*time.Minute)) || body.OldKeyExpiresAt.After(started.Add(24*time.Hour+time.Minute)) {
		t.Fatalf("invalid rotation response: %+v", body)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRevokeKeyAndAuditCommitTogether(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	server := NewServer(db, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	mock.ExpectBegin()
	mock.ExpectExec(`UPDATE api_keys SET revoked_at`).WithArgs(uint64(8)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`INSERT INTO admin_audit_logs`).WithArgs("2", "root@example.test", "key.revoke", "api_key", "8", sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	req := httptest.NewRequest("POST", "/v1/api-keys/8/revoke", nil)
	req.SetPathValue("id", "8")
	req.Header.Set("X-InaTEWS-Admin-ID", "2")
	req.Header.Set("X-InaTEWS-Admin-Email", "root@example.test")
	recorder := httptest.NewRecorder()
	server.revokeKey(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizePrivilegesRejectsUnknownValues(t *testing.T) {
	got := normalizePrivileges([]string{"admin:*", "earthquakes:read", "earthquakes:read"})
	if len(got) != 1 || got[0] != "earthquakes:read" {
		t.Fatalf("unexpected privileges: %#v", got)
	}
}

func TestAPIMetricsAggregatesRequestsErrorsAndLatency(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	server := NewServer(db, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	bucket := time.Now().UTC().Add(-time.Minute).Truncate(time.Minute)
	mock.ExpectQuery(`SELECT minute_bucket, SUM\(request_count\)`).WithArgs(sqlmock.AnyArg()).WillReturnRows(
		sqlmock.NewRows([]string{"minute_bucket", "requests", "c4", "c5", "limited", "duration", "max"}).AddRow(bucket, 10, 2, 1, 1, 500, 120),
	)
	mock.ExpectQuery(`SELECT method,route,SUM\(request_count\)`).WithArgs(sqlmock.AnyArg()).WillReturnRows(
		sqlmock.NewRows([]string{"method", "route", "requests", "c4", "c5", "duration", "max"}).AddRow("GET", "/v1/earthquakes", 10, 2, 1, 500, 120),
	)
	mock.ExpectQuery(`SELECT m.api_key_id`).WithArgs(sqlmock.AnyArg()).WillReturnRows(
		sqlmock.NewRows([]string{"api_key_id", "name", "prefix", "requests", "duration", "last_used_at"}).AddRow(3, "mobile", "inatews_live_abc", 10, 500, bucket),
	)
	recorder := httptest.NewRecorder()
	server.apiMetrics(recorder, httptest.NewRequest("GET", "/v1/api/metrics?window=1h", nil))
	if recorder.Code != 200 {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)
	summary := body["summary"].(map[string]any)
	if summary["requests"] != float64(10) || summary["avg_duration_ms"] != float64(50) || summary["rate_limited"] != float64(1) {
		t.Fatalf("unexpected summary: %#v", summary)
	}
}

func TestEntityStatsAlwaysReturnsAllEightEntityTypes(t *testing.T) {
	db, mock, _ := sqlmock.New()
	defer db.Close()
	server := NewServer(db, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	mock.ExpectQuery(`SELECT entity_type`).WillReturnRows(
		sqlmock.NewRows([]string{"entity_type", "active", "deleted", "sequence", "latest"}).AddRow("earthquake", 12, 1, 99, time.Now().UTC()),
	)
	stats := server.entityStats(context.Background())
	if len(stats) != 8 || stats[0]["entity_type"] != "earthquake" || stats[7]["entity_type"] != "eq_phase" {
		t.Fatalf("unexpected entity stats: %#v", stats)
	}
}

func stringify(value any) string {
	body, _ := json.Marshal(value)
	return string(body)
}
