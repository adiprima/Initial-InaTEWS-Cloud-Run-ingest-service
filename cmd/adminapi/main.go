package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/inatews/inatews-cloud-run/internal/adminapi"
	"github.com/inatews/inatews-cloud-run/internal/config"
	"github.com/inatews/inatews-cloud-run/internal/database"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid admin API configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := database.Open(ctx, cfg)
	if err != nil {
		logger.Error("admin API database initialization failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	api := adminapi.NewServer(db, logger, adminapi.NewGCPMetricsFromEnv())
	server := &http.Server{Addr: ":" + cfg.Port, Handler: api.Handler(), ReadHeaderTimeout: cfg.HTTPReadTimeout, WriteTimeout: cfg.HTTPWriteTimeout, IdleTimeout: cfg.HTTPIdleTimeout}
	errorsCh := make(chan error, 1)
	go func() { logger.Info("admin API listening", "port", cfg.Port); errorsCh <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	case err := <-errorsCh:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("admin API stopped", "error", err)
			os.Exit(1)
		}
	}
}
