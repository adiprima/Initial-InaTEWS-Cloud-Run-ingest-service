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

	"github.com/inatews/inatews-cloud-run/internal/config"
	"github.com/inatews/inatews-cloud-run/internal/database"
	"github.com/inatews/inatews-cloud-run/internal/publicapi"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	dbConfig, err := config.Load()
	if err != nil {
		logger.Error("invalid API configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := database.Open(ctx, dbConfig)
	if err != nil {
		logger.Error("API database initialization failed", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	api := publicapi.NewServer(db, publicapi.NewSQLRepository(db), publicapi.NewSQLAuthorizer(db), logger, publicapi.LoadConfig())
	server := &http.Server{
		Addr:              ":" + dbConfig.Port,
		Handler:           api.Handler(),
		ReadHeaderTimeout: dbConfig.HTTPReadTimeout,
		WriteTimeout:      dbConfig.HTTPWriteTimeout,
		IdleTimeout:       dbConfig.HTTPIdleTimeout,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("public API server listening", "port", dbConfig.Port)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("public API graceful shutdown failed", "error", err)
		}
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("public API server stopped", "error", err)
			os.Exit(1)
		}
	}
}
