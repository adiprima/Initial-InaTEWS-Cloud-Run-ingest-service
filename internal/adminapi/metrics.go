package adminapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type GCPMetrics struct {
	projectID         string
	subscriptionID    string
	dlqSubscriptionID string
	httpClient        *http.Client
	metadataTokenURL  string
	publicAPIService  string
	ingestService     string
	region            string
	mu                sync.Mutex
	cached            map[string]any
	cachedAt          time.Time
}

func NewGCPMetricsFromEnv() *GCPMetrics {
	return &GCPMetrics{
		projectID: os.Getenv("GCP_PROJECT_ID"), subscriptionID: os.Getenv("GCP_SYNC_SUBSCRIPTION_ID"),
		dlqSubscriptionID: os.Getenv("GCP_DLQ_SUBSCRIPTION_ID"), httpClient: &http.Client{Timeout: 5 * time.Second},
		metadataTokenURL: "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token",
		publicAPIService: os.Getenv("PUBLIC_API_SERVICE"), ingestService: os.Getenv("INGEST_SERVICE"),
		region: os.Getenv("GCP_REGION"),
	}
}

func (m *GCPMetrics) Snapshot(ctx context.Context) map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cached != nil && time.Since(m.cachedAt) < 30*time.Second {
		return m.cached
	}
	m.cached = m.snapshot(ctx)
	m.cachedAt = time.Now()
	return m.cached
}

func (m *GCPMetrics) snapshot(ctx context.Context) map[string]any {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	result := map[string]any{"available": false, "service": os.Getenv("K_SERVICE"), "revision": os.Getenv("K_REVISION")}
	if m.projectID == "" || m.subscriptionID == "" {
		result["error"] = "GCP metrics configuration is incomplete"
		return result
	}
	token, err := m.accessToken(ctx)
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	main, err := m.subscriptionMetrics(ctx, token, m.subscriptionID)
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	result["available"] = true
	result["subscription"] = main
	result["pubsub"] = map[string]any{"subscription_id": m.subscriptionID, "backlog": main["undelivered_messages"], "oldest_unacked_seconds": main["oldest_unacked_seconds"]}
	if m.dlqSubscriptionID != "" {
		if dlq, err := m.subscriptionMetrics(ctx, token, m.dlqSubscriptionID); err == nil {
			result["dead_letter_subscription"] = dlq
			result["dlq"] = map[string]any{"subscription_id": m.dlqSubscriptionID, "backlog": dlq["undelivered_messages"], "oldest_unacked_seconds": dlq["oldest_unacked_seconds"]}
		}
	}
	services := map[string]any{}
	for _, service := range []string{m.publicAPIService, m.ingestService} {
		if service == "" {
			continue
		}
		services[service] = m.cloudRunMetrics(ctx, token, service)
	}
	result["cloud_run"] = services
	return result
}

func (m *GCPMetrics) cloudRunMetrics(ctx context.Context, token, service string) map[string]any {
	base := fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s"`, service)
	result := map[string]any{}
	queries := map[string]string{
		"requests_latest_point":        `metric.type="run.googleapis.com/request_count" AND ` + base,
		"client_errors_latest_point":   `metric.type="run.googleapis.com/request_count" AND metric.labels.response_code_class="4xx" AND ` + base,
		"server_errors_latest_point":   `metric.type="run.googleapis.com/request_count" AND metric.labels.response_code_class="5xx" AND ` + base,
		"latency_mean_ms_latest_point": `metric.type="run.googleapis.com/request_latencies" AND ` + base,
	}
	for name, filter := range queries {
		if value, err := m.latestValueFilter(ctx, token, filter); err == nil {
			result[name] = value
		}
	}
	if serviceData, err := m.cloudRunService(ctx, token, service); err == nil {
		for key, value := range serviceData {
			result[key] = value
		}
	}
	return result
}

func (m *GCPMetrics) cloudRunService(ctx context.Context, token, service string) (map[string]any, error) {
	if m.region == "" {
		return nil, fmt.Errorf("GCP_REGION is missing")
	}
	endpoint := fmt.Sprintf("https://run.googleapis.com/v2/projects/%s/locations/%s/services/%s", url.PathEscape(m.projectID), url.PathEscape(m.region), url.PathEscape(service))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Cloud Run API HTTP %d", resp.StatusCode)
	}
	var body struct {
		LatestReadyRevision   string `json:"latestReadyRevision"`
		LatestCreatedRevision string `json:"latestCreatedRevision"`
		Traffic               []any  `json:"traffic"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return map[string]any{"latest_ready_revision": body.LatestReadyRevision, "latest_created_revision": body.LatestCreatedRevision, "traffic": body.Traffic}, nil
}

func (m *GCPMetrics) accessToken(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, m.metadataTokenURL, nil)
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("metadata token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("metadata token HTTP %d", resp.StatusCode)
	}
	var body struct {
		AccessToken string `json:"access_token"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil || body.AccessToken == "" {
		return "", fmt.Errorf("metadata token response is invalid")
	}
	return body.AccessToken, nil
}

func (m *GCPMetrics) subscriptionMetrics(ctx context.Context, token, subscription string) (map[string]any, error) {
	metrics := map[string]string{"undelivered_messages": "pubsub.googleapis.com/subscription/num_undelivered_messages", "oldest_unacked_seconds": "pubsub.googleapis.com/subscription/oldest_unacked_message_age"}
	result := map[string]any{"subscription_id": subscription}
	for name, metric := range metrics {
		value, err := m.latestValue(ctx, token, metric, subscription)
		if err != nil {
			return nil, err
		}
		result[name] = value
	}
	return result, nil
}

func (m *GCPMetrics) latestValue(ctx context.Context, token, metric, subscription string) (float64, error) {
	filter := fmt.Sprintf(`metric.type="%s" AND resource.labels.subscription_id="%s"`, metric, subscription)
	return m.latestValueFilter(ctx, token, filter)
}

func (m *GCPMetrics) latestValueFilter(ctx context.Context, token, filter string) (float64, error) {
	end := time.Now().UTC()
	start := end.Add(-10 * time.Minute)
	query := url.Values{"filter": {filter}, "interval.startTime": {start.Format(time.RFC3339)}, "interval.endTime": {end.Format(time.RFC3339)}, "view": {"FULL"}}
	endpoint := "https://monitoring.googleapis.com/v3/projects/" + url.PathEscape(m.projectID) + "/timeSeries?" + query.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("Cloud Monitoring HTTP %d", resp.StatusCode)
	}
	var body struct {
		TimeSeries []struct {
			Points []struct {
				Value map[string]any `json:"value"`
			} `json:"points"`
		} `json:"timeSeries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, err
	}
	if len(body.TimeSeries) == 0 {
		return 0, nil
	}
	var total float64
	for _, series := range body.TimeSeries {
		if len(series.Points) == 0 {
			continue
		}
		for _, value := range series.Points[0].Value {
			switch typed := value.(type) {
			case string:
				var parsed float64
				fmt.Sscan(typed, &parsed)
				total += parsed
			case float64:
				total += typed
			case map[string]any:
				if mean, ok := typed["mean"].(float64); ok {
					total += mean
				}
			}
		}
	}
	return total, nil
}

func sanitize(value string) string { return strings.TrimSpace(value) }
