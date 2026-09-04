package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/inatews/inatews-cloud-run/internal/config"
	"github.com/inatews/inatews-cloud-run/internal/database"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	rawKey := strings.TrimSpace(os.Getenv("API_KEY"))
	clientName := strings.TrimSpace(os.Getenv("API_CLIENT_NAME"))
	keyName := strings.TrimSpace(os.Getenv("API_KEY_NAME"))
	if len(rawKey) < 24 || clientName == "" || keyName == "" {
		logger.Error("API_KEY, API_CLIENT_NAME, and API_KEY_NAME are required")
		os.Exit(1)
	}
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	db, err := database.Open(context.Background(), cfg)
	if err != nil {
		logger.Error("database initialization failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := seed(context.Background(), db, rawKey, clientName, keyName); err != nil {
		logger.Error("API key seed failed", "error", err)
		os.Exit(1)
	}
	logger.Info("API key hash is ready", "client", clientName, "key_name", keyName)
}

func seed(ctx context.Context, db *sql.DB, rawKey, clientName, keyName string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var clientID uint64
	err = tx.QueryRowContext(ctx, `SELECT id FROM api_clients WHERE name = ? ORDER BY id LIMIT 1`, clientName).Scan(&clientID)
	if errors.Is(err, sql.ErrNoRows) {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO api_clients (name, status, default_rate_limit_per_minute) VALUES (?, 'active', 60)`, clientName)
		if insertErr != nil {
			return fmt.Errorf("create API client: %w", insertErr)
		}
		id, insertErr := result.LastInsertId()
		if insertErr != nil {
			return fmt.Errorf("read API client ID: %w", insertErr)
		}
		clientID = uint64(id)
	} else if err != nil {
		return fmt.Errorf("lookup API client: %w", err)
	}

	hash := sha256.Sum256([]byte(rawKey))
	prefix := rawKey
	if len(prefix) > 16 {
		prefix = prefix[:16]
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO api_keys (client_id, key_prefix, key_hash, name, privileges)
		VALUES (?, ?, ?, ?, JSON_ARRAY('earthquakes:read'))
		ON DUPLICATE KEY UPDATE
			client_id = VALUES(client_id), name = VALUES(name),
			privileges = VALUES(privileges), revoked_at = NULL`, clientID, prefix, hash[:], keyName)
	if err != nil {
		return fmt.Errorf("upsert API key hash: %w", err)
	}
	return tx.Commit()
}
