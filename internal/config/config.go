package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port              string
	DBUser            string
	DBPassword        string
	DBName            string
	DBSocket          string
	DBHost            string
	DBPort            string
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration
	HTTPReadTimeout   time.Duration
	HTTPWriteTimeout  time.Duration
	HTTPIdleTimeout   time.Duration
	MaxRequestBytes   int64
}

func Load() (Config, error) {
	cfg := Config{
		Port:              env("PORT", "8080"),
		DBUser:            strings.TrimSpace(os.Getenv("DB_USER")),
		DBPassword:        os.Getenv("DB_PASSWORD"),
		DBName:            strings.TrimSpace(os.Getenv("DB_NAME")),
		DBSocket:          strings.TrimSpace(os.Getenv("DB_SOCKET")),
		DBHost:            strings.TrimSpace(os.Getenv("DB_HOST")),
		DBPort:            env("DB_PORT", "3306"),
		DBMaxOpenConns:    envInt("DB_MAX_OPEN_CONNS", 7),
		DBMaxIdleConns:    envInt("DB_MAX_IDLE_CONNS", 5),
		DBConnMaxLifetime: envDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute),
		HTTPReadTimeout:   envDuration("HTTP_READ_TIMEOUT", 15*time.Second),
		HTTPWriteTimeout:  envDuration("HTTP_WRITE_TIMEOUT", 30*time.Second),
		HTTPIdleTimeout:   envDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
		MaxRequestBytes:   envInt64("MAX_REQUEST_BYTES", 15*1024*1024),
	}

	if cfg.DBUser == "" {
		return Config{}, fmt.Errorf("DB_USER is required")
	}
	if cfg.DBPassword == "" {
		return Config{}, fmt.Errorf("DB_PASSWORD is required")
	}
	if cfg.DBName == "" {
		return Config{}, fmt.Errorf("DB_NAME is required")
	}
	if cfg.DBSocket == "" && cfg.DBHost == "" {
		return Config{}, fmt.Errorf("DB_SOCKET or DB_HOST is required")
	}
	if cfg.DBMaxOpenConns < 1 || cfg.DBMaxIdleConns < 0 || cfg.DBMaxIdleConns > cfg.DBMaxOpenConns {
		return Config{}, fmt.Errorf("invalid database pool limits")
	}
	if cfg.MaxRequestBytes < 1024 {
		return Config{}, fmt.Errorf("MAX_REQUEST_BYTES must be at least 1024")
	}

	return cfg, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
