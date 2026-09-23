package publicapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrUnauthorized = errors.New("invalid API key")
	ErrForbidden    = errors.New("API key lacks required privilege")
	ErrRateLimited  = errors.New("API rate limit exceeded")
)

type Authorizer interface {
	Authorize(context.Context, string, string) (Principal, error)
}

type SQLAuthorizer struct {
	db *sql.DB
}

func NewSQLAuthorizer(db *sql.DB) *SQLAuthorizer {
	return &SQLAuthorizer{db: db}
}

func (a *SQLAuthorizer) Authorize(ctx context.Context, rawKey, requiredPrivilege string) (Principal, error) {
	if len(rawKey) < 24 {
		return Principal{}, ErrUnauthorized
	}
	hash := sha256.Sum256([]byte(rawKey))
	var principal Principal
	var privilegesRaw []byte
	var keyLimit sql.NullInt64
	var defaultLimit uint64
	var expiresAt sql.NullTime
	var suspendedAt sql.NullTime
	var revokedAt sql.NullTime
	var clientStatus string
	err := a.db.QueryRowContext(ctx, `
		SELECT k.id, c.id, c.name, c.status, k.privileges,
		       k.rate_limit_per_minute, c.default_rate_limit_per_minute,
		       k.expires_at, k.suspended_at, k.revoked_at
		FROM api_keys k
		JOIN api_clients c ON c.id = k.client_id
		WHERE k.key_hash = ?
		LIMIT 1`, hash[:]).Scan(
		&principal.APIKeyID, &principal.ClientID, &principal.ClientName,
		&clientStatus, &privilegesRaw, &keyLimit, &defaultLimit,
		&expiresAt, &suspendedAt, &revokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, fmt.Errorf("lookup API key: %w", err)
	}
	if clientStatus != "active" || suspendedAt.Valid || revokedAt.Valid || (expiresAt.Valid && !expiresAt.Time.After(time.Now().UTC())) {
		return Principal{}, ErrUnauthorized
	}

	var privilegeList []string
	if err := json.Unmarshal(privilegesRaw, &privilegeList); err != nil {
		return Principal{}, fmt.Errorf("decode API privileges: %w", err)
	}
	principal.Privileges = make(map[string]bool, len(privilegeList))
	for _, privilege := range privilegeList {
		principal.Privileges[privilege] = true
	}
	if !principal.Privileges["*"] && !principal.Privileges[requiredPrivilege] {
		return Principal{}, ErrForbidden
	}
	principal.RateLimit = defaultLimit
	if keyLimit.Valid {
		principal.RateLimit = uint64(keyLimit.Int64)
	}
	if principal.RateLimit == 0 {
		principal.RateLimit = 60
	}
	if err := a.consume(ctx, principal); err != nil {
		return Principal{}, err
	}
	return principal, nil
}

func (a *SQLAuthorizer) consume(ctx context.Context, principal Principal) error {
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin rate-limit transaction: %w", err)
	}
	defer tx.Rollback()

	bucket := time.Now().UTC().Truncate(time.Minute)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO api_usage_minutes (api_key_id, minute_bucket, request_count)
		VALUES (?, ?, 1)
		ON DUPLICATE KEY UPDATE request_count = request_count + 1`, principal.APIKeyID, bucket)
	if err != nil {
		return fmt.Errorf("increment API usage: %w", err)
	}
	var count uint64
	if err := tx.QueryRowContext(ctx, `
		SELECT request_count FROM api_usage_minutes
		WHERE api_key_id = ? AND minute_bucket = ? FOR UPDATE`, principal.APIKeyID, bucket).Scan(&count); err != nil {
		return fmt.Errorf("read API usage: %w", err)
	}
	if count > principal.RateLimit {
		return ErrRateLimited
	}
	if _, err := tx.ExecContext(ctx, `UPDATE api_keys SET last_used_at = CURRENT_TIMESTAMP(6) WHERE id = ?`, principal.APIKeyID); err != nil {
		return fmt.Errorf("update API key usage: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit API usage: %w", err)
	}
	return nil
}
