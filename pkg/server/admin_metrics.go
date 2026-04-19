package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/tenant"
)

const (
	monitoringBaseURL    = "https://monitoring.googleapis.com/v3/projects"
	metadataTokenURL     = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token" //nolint:gosec // GCP metadata URL, not a credential
	metadataProjectURL   = "http://metadata.google.internal/computeMetadata/v1/project/project-id"
	anthropicMessagesURL = "https://api.anthropic.com/v1/messages"
	anthropicVersion     = "2023-06-01"
	defaultInsightsModel = "claude-sonnet-4-6"
	metricsHTTPTimeout   = 10 * time.Second
	analysisHTTPTimeout  = 30 * time.Second
	handlerTimeout       = 50 * time.Second
	analysisMaxTokens    = 1024
	defaultMetricDays    = 1
	maxMetricDays        = 30
)

var (
	metricsHTTPClient  = &http.Client{Timeout: metricsHTTPTimeout}
	analysisHTTPClient = &http.Client{Timeout: analysisHTTPTimeout}
	metricDayOptions   = []int{1, 2, 7, 14, 30}
)

// adminMetricsConfig holds config for GCP monitoring and Claude analysis.
type adminMetricsConfig struct {
	projectID    string
	anthropicKey string
	model        string
	service      string
}

func newAdminMetricsConfig() *adminMetricsConfig {
	projectID := config.GetEnv("GCP_PROJECT_ID", "")
	if projectID == "" {
		// Try GCE metadata server (works on Cloud Run without extra env vars).
		projectID = gcpMetadataProjectID()
	}
	if projectID == "" {
		return nil
	}

	key := config.GetEnv("DEVTRACE_ANTHROPIC_API_KEY", "")
	if key == "" {
		key = config.GetEnv("ANTHROPIC_API_KEY", "")
	}

	model := config.GetEnv("DEVTRACE_ANTHROPIC_MODEL", "")
	if model == "" {
		model = config.GetEnv("ANTHROPIC_MODEL", defaultInsightsModel)
	}

	// K_SERVICE is auto-set by Cloud Run to the actual service name.
	service := config.GetEnv("K_SERVICE", config.GetEnv("DEVTRACE_SERVICE_NAME", "devtrace-saas-serve"))

	return &adminMetricsConfig{
		projectID:    projectID,
		anthropicKey: key,
		model:        model,
		service:      service,
	}
}

// gcpMetadataProjectID fetches the project ID from the GCE metadata server.
// Returns empty string when not running on GCP.
func gcpMetadataProjectID() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataProjectURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := metricsHTTPClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}

// gcpAccessToken fetches an access token from the GCE metadata server.
func gcpMetadataToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataTokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Metadata-Flavor", "Google")

	resp, err := metricsHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("metadata server returned %d", resp.StatusCode)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("decoding token: %w", err)
	}
	return tok.AccessToken, nil
}

// queryTimeSeries calls the GCP Monitoring API for a single metric filter.
func queryTimeSeries(ctx context.Context, projectID, token, filter, params string, start, end time.Time) (string, error) {
	encoded := url.QueryEscape(filter)
	u := fmt.Sprintf("%s/%s/timeSeries?filter=%s&interval.startTime=%s&interval.endTime=%s&%s",
		monitoringBaseURL, projectID, encoded,
		start.Format(time.RFC3339), end.Format(time.RFC3339), params)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil) //nolint:gosec // URL from trusted config
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := metricsHTTPClient.Do(req) //nolint:gosec // URL from trusted config
	if err != nil {
		return "", fmt.Errorf("querying metrics: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("monitoring API returned %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}

	return string(body), nil
}

type metricQuery struct {
	label  string
	filter string
	params string
}

func adminMetricQueries(cfg *adminMetricsConfig, hourlyAlign string) []metricQuery {
	svc := cfg.service
	return []metricQuery{
		{
			label:  "Request Count (req/s by response class)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_count"`, svc),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_RATE&aggregation.crossSeriesReducer=REDUCE_SUM&aggregation.groupByFields=metric.labels.response_code_class",
		},
		{
			label:  "Request Latency p50 (ms)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_latencies"`, svc),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_50&aggregation.crossSeriesReducer=REDUCE_MEAN",
		},
		{
			label:  "Request Latency p99 (ms)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_latencies"`, svc),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MEAN",
		},
		{
			label:  "Instance Count",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/container/instance_count"`, svc),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_MAX&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label:  "CPU Utilization",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/container/cpu/utilizations"`, svc),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MAX",
		},
		{
			label:  "Memory Utilization",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/container/memory/utilizations"`, svc),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MAX",
		},
		{
			label: "Application Errors (hourly)",
			filter: fmt.Sprintf(
				`resource.type="cloud_run_revision" AND resource.labels.service_name="%s"`+
					` AND metric.type="logging.googleapis.com/log_entry_count" AND metric.labels.severity="ERROR"`,
				svc),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
	}
}

type queryResult struct {
	label string
	body  string
	err   error
}

// fetchQueries runs all metric queries concurrently and returns results in order.
func fetchQueries(ctx context.Context, projectID, token string, queries []metricQuery, start, end time.Time) []queryResult {
	results := make([]queryResult, len(queries))
	var wg sync.WaitGroup
	wg.Add(len(queries))
	for i, q := range queries {
		go func(idx int, mq metricQuery) {
			defer wg.Done()
			raw, err := queryTimeSeries(ctx, projectID, token, mq.filter, mq.params, start, end)
			results[idx] = queryResult{label: mq.label, body: raw, err: err}
		}(i, q)
	}
	wg.Wait()
	return results
}

func collectGCPMetrics(ctx context.Context, cfg *adminMetricsConfig, token string, days int) string {
	now := time.Now().UTC()
	start := now.Add(-time.Duration(days) * 24 * time.Hour)
	hourlyAlign := "aggregation.alignmentPeriod=3600s"

	var b strings.Builder
	fmt.Fprintf(&b, "DevTrace Cloud Metrics — Last %d day(s), Project: %s, %s\n",
		days, cfg.projectID, now.Format("2006-01-02 15:04 UTC"))
	b.WriteString("NOTE: Cloud Run metrics are service-wide. Admin/metrics page requests are included and may cause brief spikes.\n\n")

	for _, r := range fetchQueries(ctx, cfg.projectID, token, adminMetricQueries(cfg, hourlyAlign), start, now) {
		if r.err != nil {
			fmt.Fprintf(&b, "--- %s ---\n  (error: %s)\n\n", r.label, r.err)
			continue
		}
		fmt.Fprintf(&b, "--- %s ---\n%s\n\n", r.label, formatTimeSeries(r.body))
	}

	return b.String()
}

func collectDBMetrics(ctx context.Context, store *postgres.Store, db *sql.DB, days int) string {
	var b strings.Builder
	b.WriteString("=== Internal DB Metrics ===\n\n")

	if ps, err := store.PipelineStats(ctx); err == nil {
		fmt.Fprintf(&b, "--- Pipeline Health ---\n")
		fmt.Fprintf(&b, "  Last Ingest: %s\n", formatTimestamp(ps.LastIngest))
		fmt.Fprintf(&b, "  Last Scored: %s\n", formatTimestamp(ps.LastScored))
		fmt.Fprintf(&b, "  Total Activity Records: %d\n\n", ps.TotalActivities)
	}

	if depth, err := store.QueueDepth(ctx); err == nil {
		fmt.Fprintf(&b, "--- Scoring Queue ---\n  Depth: %d\n", depth)
	}
	if stale, err := store.StaleCount(ctx, 7, 30); err == nil {
		fmt.Fprintf(&b, "  Stale (7-30d): %d\n", stale)
	}
	if cc, err := store.ContributorCount(ctx); err == nil {
		fmt.Fprintf(&b, "  Registered Contributors (scored at least once): %d\n", cc)
	}
	if sc, err := store.ScoredCount(ctx); err == nil {
		fmt.Fprintf(&b, "  With Current Score: %d\n\n", sc)
	}

	if hc, err := store.HourlyScoringCounts(ctx, 24); err == nil && len(hc) > 0 {
		fmt.Fprintf(&b, "--- Scoring Throughput (24h) ---\n")
		for _, h := range hc {
			fmt.Fprintf(&b, "  %s: %d scores\n", h.Day.Format("15:04"), h.Count)
		}
		b.WriteString("\n")
	}

	if ac, err := store.HourlyActivityCounts(ctx, 24); err == nil && len(ac) > 0 {
		fmt.Fprintf(&b, "--- Ingestion Throughput (24h) ---\n")
		for _, h := range ac {
			fmt.Fprintf(&b, "  %s: %d records\n", h.Day.Format("15:04"), h.Count)
		}
		b.WriteString("\n")
	}

	since := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	if samples, err := store.GetTokenQuotaSamples(ctx, since); err == nil && len(samples) > 0 {
		fmt.Fprintf(&b, "--- Token Pool Quota (%dd) ---\n", days)
		// Show latest sample per installation.
		latest := make(map[int64]postgres.TokenQuotaSample)
		for _, s := range samples {
			latest[s.InstallationID] = s
		}
		var totalLimit, totalUsed int
		for _, s := range latest {
			fmt.Fprintf(&b, "  %s (inst %d): %d/%d used\n", s.Login, s.InstallationID, s.QuotaUsed, s.QuotaLimit)
			totalLimit += s.QuotaLimit
			totalUsed += s.QuotaUsed
		}
		if totalLimit > 0 {
			fmt.Fprintf(&b, "  Aggregate: %d/%d used (%.1f%%)\n", totalUsed, totalLimit, float64(totalUsed)/float64(totalLimit)*100)
		}
		b.WriteString("\n")
	}

	if tenants, err := tenant.ListTenants(ctx, db); err == nil {
		fmt.Fprintf(&b, "--- Tenants ---\n  Total: %d\n\n", len(tenants))
	}

	return b.String()
}

func formatTimestamp(t time.Time) string {
	if t.IsZero() {
		return durationNever
	}
	return t.Format("2006-01-02 15:04 UTC")
}

// formatTimeSeries extracts data points from the monitoring API JSON response.
func formatTimeSeries(raw string) string {
	var resp struct {
		TimeSeries []struct {
			Metric struct {
				Labels map[string]string `json:"labels"`
			} `json:"metric"`
			Points []struct {
				Interval struct {
					StartTime string `json:"startTime"`
				} `json:"interval"`
				Value struct {
					DoubleValue *float64 `json:"doubleValue"`
					Int64Value  *string  `json:"int64Value"`
				} `json:"value"`
			} `json:"points"`
		} `json:"timeSeries"`
	}

	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return "  (parse error)"
	}

	if len(resp.TimeSeries) == 0 {
		return "  (no data)"
	}

	var b strings.Builder
	for _, ts := range resp.TimeSeries {
		var prefix string
		if len(ts.Metric.Labels) > 0 {
			var parts []string
			for _, v := range ts.Metric.Labels {
				parts = append(parts, v)
			}
			prefix = strings.Join(parts, " ") + ": "
		}

		limit := min(8, len(ts.Points))
		var vals []string
		for _, p := range ts.Points[:limit] {
			t := p.Interval.StartTime
			if len(t) > 16 {
				t = t[5:16]
			}
			var v float64
			if p.Value.DoubleValue != nil {
				v = *p.Value.DoubleValue
			} else if p.Value.Int64Value != nil {
				if iv, err := strconv.ParseFloat(*p.Value.Int64Value, 64); err == nil {
					v = iv
				}
			}
			vals = append(vals, fmt.Sprintf("%s=%.2f", t, v))
		}
		fmt.Fprintf(&b, "  %s%s\n", prefix, strings.Join(vals, ", "))
	}
	return b.String()
}

const metricsAnalysisPrompt = `You are a health advisor for DevTrace, a multi-tenant SaaS on
Cloud Run for contributor trust scoring. Your reader is the product owner checking overall
system health — not debugging an incident.

SYSTEM CONTEXT
- Single Cloud Run service (scale-to-zero), Cloud SQL PostgreSQL.
- Background workers: hourly GH Archive ingestion, continuous scoring.
- Token pool: GitHub App installation tokens, round-robin with rate-limit handling.
- Scorer pauses when token quota drops below 30% or queue is empty.

WHAT TO IGNORE (STRICT)
- Admin/metrics page traffic: Cloud Run metrics are service-wide and include admin requests.
  Any single-hour latency spike or instance scale-up that coincides with admin activity is
  noise — IGNORE it entirely. Do not mention it.
- Cold starts: expected with scale-to-zero. Only flag if sustained above 5s across many hours.
- Contributor count gaps: "Registered Contributors" vs "With Current Score" reflects intentional
  scoping (only contributors active in tenant repos get scored). A large gap is by design.
  NEVER flag this as a coverage issue.
- Queue depth 0 with no stale count: scorer is caught up. This is healthy.
- Low weekend/off-hours traffic: normal usage pattern.
- Token quota near 0% used: means scoring is idle or caught up, not a problem.

OUTPUT FORMAT (use exactly this structure, plain text, ALL CAPS headings, no markdown)

HEALTH STATUS
One sentence: healthy, degraded, or needs attention.

WHAT LOOKS GOOD
- 2-4 bullets confirming healthy signals (ingestion running, errors low, resources stable, etc.)

WATCH LIST
- Only items trending wrong or approaching a threshold. Include what to check if it worsens.
- If nothing, write "Nothing to flag."

Quiet day = 4-6 bullets total. Bias toward reassurance. Do not pad. Do not explain what
metrics mean — the reader knows the system.`

// analyzeAdminMetrics sends metrics to the Anthropic API for analysis.
func analyzeAdminMetrics(ctx context.Context, cfg *adminMetricsConfig, metrics string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":      cfg.model,
		"max_tokens": analysisMaxTokens,
		"system":     metricsAnalysisPrompt,
		"messages":   []map[string]string{{"role": "user", "content": metrics}},
	})
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicMessagesURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", cfg.anthropicKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := analysisHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling Anthropic API: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic API returned %d: %s",
			resp.StatusCode, string(respBody[:min(len(respBody), 200)]))
	}

	var cr struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &cr); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	if len(cr.Content) == 0 {
		return "", fmt.Errorf("empty response from Anthropic API")
	}

	return cr.Content[0].Text, nil
}
