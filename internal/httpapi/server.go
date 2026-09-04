package httpapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/inatews/inatews-cloud-run/internal/ingest"
	"github.com/inatews/inatews-cloud-run/internal/model"
)

type Server struct {
	db              *sql.DB
	processor       ingest.Processor
	logger          *slog.Logger
	maxRequestBytes int64
}

type pubsubPush struct {
	Message struct {
		Data       string            `json:"data"`
		MessageID  string            `json:"messageId"`
		Attributes map[string]string `json:"attributes"`
	} `json:"message"`
	Subscription string `json:"subscription"`
}

func NewServer(db *sql.DB, processor ingest.Processor, logger *slog.Logger, maxRequestBytes int64) *Server {
	return &Server{db: db, processor: processor, logger: logger, maxRequestBytes: maxRequestBytes}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Cloud Run reserves some URL paths ending in "z", including /healthz.
	// Keep operational endpoints free of that suffix so requests reach the app.
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /ready", s.ready)
	mux.HandleFunc("POST /pubsub/push", s.pubsubPush)
	return s.logging(mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		s.logger.Error("readiness database ping failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) pubsubPush(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxRequestBytes)
	decoder := json.NewDecoder(r.Body)

	var push pubsubPush
	if err := decoder.Decode(&push); err != nil {
		s.logger.Warn("invalid pubsub wrapper", "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Pub/Sub request"})
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request must contain one JSON object"})
		return
	}
	if push.Message.Data == "" || push.Message.MessageID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Pub/Sub message data and messageId are required"})
		return
	}

	data, err := base64.StdEncoding.DecodeString(push.Message.Data)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message data is not valid base64"})
		return
	}

	var envelope model.Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message data is not valid JSON"})
		return
	}
	if envelope.MessageID == "" {
		envelope.MessageID = push.Message.MessageID
	}
	if err := envelope.NormalizeAndValidate(); err != nil {
		s.logger.Warn("invalid sync envelope", "pubsub_message_id", push.Message.MessageID, "error", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	result, err := s.processor.Process(r.Context(), envelope, push.Message.MessageID)
	if err != nil {
		if errors.Is(err, model.ErrInvalidEnvelope) {
			s.logger.Warn("permanent ingest rejection", "message_id", envelope.MessageID, "error", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		s.logger.Error("temporary ingest failure", "message_id", envelope.MessageID, "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "temporary ingest failure"})
		return
	}

	s.logger.Info("sync message handled",
		"message_id", envelope.MessageID,
		"pubsub_message_id", push.Message.MessageID,
		"entity_type", envelope.EntityType,
		"entity_key", envelope.EntityKey,
		"result", result,
	)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("extra JSON value")
	}
	return err
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
