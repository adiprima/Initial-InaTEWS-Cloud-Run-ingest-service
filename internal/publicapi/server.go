package publicapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Server struct {
	db         *sql.DB
	repository Repository
	authorizer Authorizer
	logger     *slog.Logger
	config     Config
	cleanupDay atomic.Int64
}

type principalKey struct{}

func NewServer(db *sql.DB, repository Repository, authorizer Authorizer, logger *slog.Logger, config Config) *Server {
	return &Server{db: db, repository: repository, authorizer: authorizer, logger: logger, config: config}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /ready", s.ready)
	mux.HandleFunc("GET /openapi.json", s.openAPI)
	mux.HandleFunc("GET /docs", s.swaggerUI)
	mux.Handle("GET /v1/earthquakes", s.requireAPIKey("earthquakes:read", http.HandlerFunc(s.listEarthquakes)))
	mux.Handle("GET /v1/earthquakes/{eventid}", s.requireAPIKey("earthquakes:read", http.HandlerFunc(s.getEarthquake)))
	return s.recoverPanic(s.requestLog(s.cors(mux)))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		s.logger.Error("API readiness database ping failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "service_not_ready", "Service is not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) listEarthquakes(w http.ResponseWriter, r *http.Request) {
	filter, err := s.parseFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", err.Error())
		return
	}
	page, err := s.repository.ListEarthquakes(r.Context(), filter)
	if err != nil {
		if strings.Contains(err.Error(), "invalid cursor") {
			writeError(w, http.StatusBadRequest, "invalid_cursor", "Cursor is invalid")
			return
		}
		s.logger.Error("list earthquakes failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Unable to read earthquake data")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":       page.Items,
		"pagination": map[string]any{"next_cursor": page.NextCursor, "has_more": page.HasMore, "limit": filter.Limit},
	})
}

func (s *Server) getEarthquake(w http.ResponseWriter, r *http.Request) {
	eventID := strings.TrimSpace(r.PathValue("eventid"))
	if eventID == "" || len(eventID) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_event_id", "Event ID is invalid")
		return
	}
	item, err := s.repository.GetEarthquake(r.Context(), eventID)
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Earthquake was not found")
		return
	}
	if err != nil {
		s.logger.Error("get earthquake failed", "event_id", eventID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Unable to read earthquake data")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": item})
}

func (s *Server) parseFilter(r *http.Request) (ListFilter, error) {
	query := r.URL.Query()
	filter := ListFilter{Limit: s.config.DefaultPageSize, Cursor: strings.TrimSpace(query.Get("cursor")), Status: strings.TrimSpace(query.Get("status"))}
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > s.config.MaxPageSize {
			return ListFilter{}, errors.New("limit must be between 1 and " + strconv.Itoa(s.config.MaxPageSize))
		}
		filter.Limit = parsed
	}
	var err error
	if filter.MinMag, err = optionalFloat(query.Get("min_mag")); err != nil {
		return ListFilter{}, errors.New("min_mag must be a number")
	}
	if filter.MaxMag, err = optionalFloat(query.Get("max_mag")); err != nil {
		return ListFilter{}, errors.New("max_mag must be a number")
	}
	if filter.MinMag != nil && filter.MaxMag != nil && *filter.MinMag > *filter.MaxMag {
		return ListFilter{}, errors.New("min_mag cannot exceed max_mag")
	}
	if filter.StartTime, err = optionalTime(query.Get("start_time")); err != nil {
		return ListFilter{}, errors.New("start_time must use RFC3339")
	}
	if filter.EndTime, err = optionalTime(query.Get("end_time")); err != nil {
		return ListFilter{}, errors.New("end_time must use RFC3339")
	}
	if filter.StartTime != nil && filter.EndTime != nil && filter.StartTime.After(*filter.EndTime) {
		return ListFilter{}, errors.New("start_time cannot be after end_time")
	}
	return filter, nil
}

func (s *Server) requireAPIKey(privilege string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		measured := &metricResponseWriter{ResponseWriter: w, status: http.StatusOK}
		w = measured
		rawKey := strings.TrimSpace(r.Header.Get("X-API-Key"))
		principal, err := s.authorizer.Authorize(r.Context(), rawKey, privilege)
		apiKeyID := uint64(0)
		if err == nil {
			apiKeyID = principal.APIKeyID
		}
		defer func() {
			s.recordAPIMetric(r.Context(), apiKeyID, r.Method, r.Pattern, measured.status, time.Since(started))
		}()
		switch {
		case errors.Is(err, ErrUnauthorized):
			writeError(w, http.StatusUnauthorized, "unauthorized", "A valid X-API-Key header is required")
			return
		case errors.Is(err, ErrForbidden):
			writeError(w, http.StatusForbidden, "forbidden", "API key lacks the required privilege")
			return
		case errors.Is(err, ErrRateLimited):
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "rate_limited", "API rate limit exceeded")
			return
		case err != nil:
			s.logger.Error("API authorization failed", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "Unable to authorize request")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	})
}

type metricResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *metricResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (s *Server) recordAPIMetric(ctx context.Context, apiKeyID uint64, method, route string, status int, duration time.Duration) {
	if route == "" {
		route = "unknown"
	}
	durationMS := max(duration.Milliseconds(), 0)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO api_request_minutes
			(minute_bucket, api_key_id, method, route, status_code, request_count, total_duration_ms, max_duration_ms)
		VALUES (DATE_FORMAT(UTC_TIMESTAMP(), '%Y-%m-%d %H:%i:00'), ?, ?, ?, ?, 1, ?, ?)
		ON DUPLICATE KEY UPDATE request_count=request_count+1,
			total_duration_ms=total_duration_ms+VALUES(total_duration_ms),
			max_duration_ms=GREATEST(max_duration_ms, VALUES(max_duration_ms))`,
		apiKeyID, method, route, status, durationMS, durationMS)
	if err != nil {
		s.logger.Warn("record API metric failed", "error", err)
	}
	day := time.Now().UTC().Unix() / 86400
	previous := s.cleanupDay.Load()
	if previous != day && s.cleanupDay.CompareAndSwap(previous, day) {
		if _, cleanupErr := s.db.ExecContext(ctx, `DELETE FROM api_request_minutes WHERE minute_bucket < UTC_TIMESTAMP() - INTERVAL 30 DAY`); cleanupErr != nil {
			s.logger.Warn("clean old API metrics failed", "error", cleanupErr)
		}
		if _, cleanupErr := s.db.ExecContext(ctx, `DELETE FROM api_usage_minutes WHERE minute_bucket < UTC_TIMESTAMP() - INTERVAL 30 DAY`); cleanupErr != nil {
			s.logger.Warn("clean old API rate-limit buckets failed", "error", cleanupErr)
		}
	}
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (s.config.AllowAllOrigins || s.config.AllowedOrigins[origin]) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-API-Key")
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("public API request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("public API panic", "error", recovered)
				writeError(w, http.StatusInternalServerError, "internal_error", "Internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func optionalFloat(value string) (*float64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func optionalTime(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSONStatus(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	writeJSONStatus(w, status, payload)
}

func writeJSONStatus(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
