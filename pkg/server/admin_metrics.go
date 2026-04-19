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
	"time"

	"github.com/thingzio/devtrace/pkg/config"
	"github.com/thingzio/devtrace/pkg/data/postgres"
	"github.com/thingzio/devtrace/pkg/tenant"
)

const (
	monitoringBaseURL    = "https://monitoring.googleapis.com/v3/projects"
	metadataTokenURL     = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token" //nolint:gosec // GCP metadata URL, not a credential
	anthropicMessagesURL = "https://api.anthropic.com/v1/messages"
	anthropicVersion     = "2023-06-01"
	defaultInsightsModel = "claude-sonnet-4-6"
	metricsHTTPTimeout   = 30 * time.Second
	analysisHTTPTimeout  = 90 * time.Second
	analysisMaxTokens    = 2048
	defaultMetricDays    = 2
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

	return &adminMetricsConfig{
		projectID:    projectID,
		anthropicKey: key,
		model:        model,
		service:      "devtrace-saas",
	}
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
	return []metricQuery{
		{
			label:  "Service: Request Count (req/s by response class)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_count"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_RATE&aggregation.crossSeriesReducer=REDUCE_SUM&aggregation.groupByFields=metric.labels.response_code_class",
		},
		{
			label:  "Service: Request Latency p50 (ms)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_latencies"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_50&aggregation.crossSeriesReducer=REDUCE_MEAN",
		},
		{
			label:  "Service: Request Latency p95 (ms)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_latencies"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_95&aggregation.crossSeriesReducer=REDUCE_MEAN",
		},
		{
			label:  "Service: Request Latency p99 (ms)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_latencies"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MEAN",
		},
		{
			label:  "Service: Instance Count",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/container/instance_count"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_MAX&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label:  "Service: Startup Latency (ms, cold starts)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/container/startup_latencies"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MAX",
		},
		{
			label:  "Service: CPU Utilization",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/container/cpu/utilizations"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MAX",
		},
		{
			label:  "Service: Memory Utilization",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/container/memory/utilizations"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MAX",
		},
		{
			label:  "Service: Billable Instance Time (s/s)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/container/billable_instance_time"`, cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_RATE&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label: "Application Errors (hourly)",
			filter: fmt.Sprintf(
				`resource.type="cloud_run_revision" AND resource.labels.service_name="%s"`+
					` AND metric.type="logging.googleapis.com/log_entry_count" AND metric.labels.severity="ERROR"`,
				cfg.service),
			params: hourlyAlign + "&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
	}
}

const trendDays = 7

func adminTrendQueries(cfg *adminMetricsConfig) []metricQuery {
	daily := "aggregation.alignmentPeriod=86400s"
	return []metricQuery{
		{
			label:  "Service: Request Count (daily total)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_count"`, cfg.service),
			params: daily + "&aggregation.perSeriesAligner=ALIGN_DELTA&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
		{
			label:  "Service: Request Latency p99 (daily max, ms)",
			filter: fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s" AND metric.type="run.googleapis.com/request_latencies"`, cfg.service),
			params: daily + "&aggregation.perSeriesAligner=ALIGN_PERCENTILE_99&aggregation.crossSeriesReducer=REDUCE_MAX",
		},
		{
			label: "Application Errors (daily total)",
			filter: fmt.Sprintf(
				`resource.type="cloud_run_revision" AND resource.labels.service_name="%s"`+
					` AND metric.type="logging.googleapis.com/log_entry_count" AND metric.labels.severity="ERROR"`,
				cfg.service),
			params: daily + "&aggregation.perSeriesAligner=ALIGN_SUM&aggregation.crossSeriesReducer=REDUCE_SUM",
		},
	}
}

func collectGCPMetrics(ctx context.Context, cfg *adminMetricsConfig, token string, days int) string {
	now := time.Now().UTC()
	start := now.Add(-time.Duration(days) * 24 * time.Hour)
	hourlyAlign := "aggregation.alignmentPeriod=3600s"

	var b strings.Builder
	fmt.Fprintf(&b, "DevTrace Cloud Metrics — Last %d day(s), Project: %s, %s\n\n",
		days, cfg.projectID, now.Format("2006-01-02 15:04 UTC"))

	for _, q := range adminMetricQueries(cfg, hourlyAlign) {
		raw, err := queryTimeSeries(ctx, cfg.projectID, token, q.filter, q.params, start, now)
		if err != nil {
			fmt.Fprintf(&b, "--- %s ---\n  (error: %s)\n\n", q.label, err)
			continue
		}
		fmt.Fprintf(&b, "--- %s ---\n%s\n\n", q.label, formatTimeSeries(raw))
	}

	if days < trendDays {
		trendStart := now.Add(-time.Duration(trendDays) * 24 * time.Hour)
		fmt.Fprintf(&b, "=== 7-Day Trend Baseline ===\n\n")
		for _, q := range adminTrendQueries(cfg) {
			raw, err := queryTimeSeries(ctx, cfg.projectID, token, q.filter, q.params, trendStart, now)
			if err != nil {
				fmt.Fprintf(&b, "--- %s ---\n  (error: %s)\n\n", q.label, err)
				continue
			}
			fmt.Fprintf(&b, "--- %s ---\n%s\n\n", q.label, formatTimeSeries(raw))
		}
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
		fmt.Fprintf(&b, "  Total Contributors: %d\n", cc)
	}
	if sc, err := store.ScoredCount(ctx); err == nil {
		fmt.Fprintf(&b, "  Scored Contributors: %d\n\n", sc)
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

const metricsAnalysisPrompt = `You are a DevOps analyst reviewing GCP infrastructure metrics
for DevTrace, a multi-tenant SaaS on Cloud Run for contributor trust scoring and risk analysis.

## Architecture
- **Service** (devtrace-saas): Single Cloud Run service with embedded background workers.
  min_instance_count=0 (scale-to-zero). Handles dashboard UI, OAuth, GitHub webhooks,
  scoring API, plus background goroutines for GH Archive ingestion and continuous scoring.
- **Ingest worker**: Fetches hourly GH Archive data, upserts contributor activity into PostgreSQL.
- **Scorer worker**: Dequeues contributors, fetches GitHub API signals, computes reputation scores.
- **Token pool**: GitHub App installation tokens from all tenants, round-robin with auto-retry
  on rate limit. Tokens auto-skip when exhausted.
- **Database**: Cloud SQL PostgreSQL.

## Known Baselines & Thresholds
- Scale-to-zero: cold starts expected after idle periods (~2-3s startup latency).
- Ingest runs hourly when ENABLE_BACKGROUND_OPS is true.
- Scorer runs continuously when queue is non-empty, pauses when aggregate token quota drops below 30%.
- Queue depth of 0 is normal when caught up.
- Some contributor staleness (7-30d without re-score) is expected and not concerning below 50%.

## DB Metrics Context
- Pipeline Health: last ingest/scored timestamps indicate worker liveness.
- Queue Depth: pending contributors to score. 0 = caught up.
- Stale (7-30d): contributors needing re-score. Normal range depends on contributor count.
- Scoring/Ingestion throughput: hourly counts show worker activity.
- Token Pool Quota: GitHub API rate limit consumption across installations.

## Suppression Rules (STRICT — do NOT mention unless the override fires)
| Signal | Steady State | Override |
|--------|-------------|----------|
| Cold-start latency | ~2-3s | 7-day average exceeds 4s |
| Low traffic periods | Normal usage cycle | Weekday traffic declines 3+ consecutive days |
| Queue depth 0 | Workers caught up | Stays >100 for 6+ hours |

## Analysis Instructions
Provide a brief, actionable analysis. Bias HARD toward brevity — a quiet day should
produce a short report, not padding. Apply the Suppression Rules strictly.

1. Key Observations — Only anomalies, threshold breaches, or multi-day trends.
   One bullet per finding. Correlate across categories (e.g. latency + scoring throughput).
   Skip ANYTHING that matches steady-state behavior in the Suppression Rules table.
2. Risks — Rate each: critical, warning, watch.
   Only include risks that are actionable. If none, say "No risks identified."
3. Actions — Concrete next steps only if risks were found. One line each.

If a "7-Day Trend Baseline" section is present, compare today's metrics against the trailing
7-day pattern. Only flag regressions that exceed normal variance.

Target: 3-8 bullet points on a normal day. Up to 15 only during incidents.
If everything looks healthy, say so in 1-2 sentences and stop.

## Output Format
Respond in plain text. Use bullet points (- ) for lists. Use ALL CAPS for section headings.
Do NOT use Markdown formatting (no #, **, or backticks).`

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
