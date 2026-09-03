package database

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/inatews/inatews-cloud-run/internal/config"
)

func Open(ctx context.Context, cfg config.Config) (*sql.DB, error) {
	network := "tcp"
	address := net.JoinHostPort(cfg.DBHost, cfg.DBPort)
	if cfg.DBSocket != "" {
		network = "unix"
		address = cfg.DBSocket
	}

	dsn := mysql.Config{
		User:                 cfg.DBUser,
		Passwd:               cfg.DBPassword,
		Net:                  network,
		Addr:                 address,
		DBName:               cfg.DBName,
		ParseTime:            true,
		Loc:                  time.UTC,
		AllowNativePasswords: true,
		Params: map[string]string{
			"charset":   "utf8mb4",
			"collation": "utf8mb4_unicode_ci",
		},
	}

	db, err := sql.Open("mysql", dsn.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql: %w", err)
	}
	db.SetMaxOpenConns(cfg.DBMaxOpenConns)
	db.SetMaxIdleConns(cfg.DBMaxIdleConns)
	db.SetConnMaxLifetime(cfg.DBConnMaxLifetime)
	db.SetConnMaxIdleTime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping mysql: %w", err)
	}

	return db, nil
}
