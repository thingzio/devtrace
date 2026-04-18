# Uptime check

resource "google_monitoring_uptime_check_config" "serve" {
  display_name = "${var.prefix}-uptime"
  timeout      = "10s"
  period       = "300s"
  project      = var.project_id

  http_check {
    path         = "/health"
    port         = 443
    use_ssl      = true
    validate_ssl = true
  }

  monitored_resource {
    type = "uptime_url"
    labels = {
      project_id = var.project_id
      host       = var.domain
    }
  }
}

# Notification channel

resource "google_monitoring_notification_channel" "email" {
  display_name = "${var.prefix}-email"
  type         = "email"
  project      = var.project_id

  labels = {
    email_address = var.notification_email
  }
}

# ---------------------------------------------------------------------------
# Log-based metrics: Archive Ingest
# ---------------------------------------------------------------------------

resource "google_logging_metric" "archive_hour_complete" {
  name    = "${var.prefix}-archive-hour-complete"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"archive hour complete\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "DISTRIBUTION"
    unit        = "s"

    labels {
      key         = "mode"
      value_type  = "STRING"
      description = "Processing mode (hourly or backfill)"
    }
  }

  value_extractor = "EXTRACT(jsonPayload.duration_sec)"

  label_extractors = {
    "mode" = "EXTRACT(jsonPayload.mode)"
  }

  bucket_options {
    explicit_buckets {
      bounds = [10, 30, 60, 120, 300, 600]
    }
  }
}

resource "google_logging_metric" "backfill_batch_complete" {
  name    = "${var.prefix}-backfill-batch-complete"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"backfill batch complete\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "DISTRIBUTION"
    unit        = "s"
  }

  value_extractor = "EXTRACT(jsonPayload.batch_duration_sec)"

  bucket_options {
    explicit_buckets {
      bounds = [30, 60, 120, 300, 600, 1800]
    }
  }
}

resource "google_logging_metric" "backfill_complete" {
  name    = "${var.prefix}-backfill-complete"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"backfill complete\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "DISTRIBUTION"
    unit        = "s"
  }

  value_extractor = "EXTRACT(jsonPayload.duration_sec)"

  bucket_options {
    explicit_buckets {
      bounds = [300, 600, 1800, 3600, 7200, 14400]
    }
  }
}

# ---------------------------------------------------------------------------
# Log-based metrics: Background Scoring
# ---------------------------------------------------------------------------

resource "google_logging_metric" "queue_scoring_complete" {
  name    = "${var.prefix}-queue-scoring-complete"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"queue scoring complete\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "DISTRIBUTION"
    unit        = "1"
  }

  value_extractor = "EXTRACT(jsonPayload.scored)"

  bucket_options {
    explicit_buckets {
      bounds = [1, 10, 25, 50, 100, 200]
    }
  }
}

resource "google_logging_metric" "stale_rescoring_complete" {
  name    = "${var.prefix}-stale-rescoring-complete"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"stale rescoring complete\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "DISTRIBUTION"
    unit        = "1"
  }

  value_extractor = "EXTRACT(jsonPayload.scored)"

  bucket_options {
    explicit_buckets {
      bounds = [1, 10, 25, 50, 100, 200]
    }
  }
}

resource "google_logging_metric" "scorer_quota_paused" {
  name    = "${var.prefix}-scorer-quota-paused"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"scorer quota paused\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"
  }
}

# ---------------------------------------------------------------------------
# Log-based metrics: HTTP Service
# ---------------------------------------------------------------------------

resource "google_logging_metric" "score_request" {
  name    = "${var.prefix}-score-request"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"score request\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "source"
      value_type  = "STRING"
      description = "Request source (api or ui)"
    }
  }

  label_extractors = {
    "source" = "EXTRACT(jsonPayload.source)"
  }
}

resource "google_logging_metric" "rate_limit_exceeded" {
  name    = "${var.prefix}-rate-limit-exceeded"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"rate limit exceeded\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "tier"
      value_type  = "STRING"
      description = "Rate limit tier (auth or unauth)"
    }

    labels {
      key         = "path"
      value_type  = "STRING"
      description = "Request path"
    }
  }

  label_extractors = {
    "tier" = "EXTRACT(jsonPayload.tier)"
    "path" = "EXTRACT(jsonPayload.path)"
  }
}

resource "google_logging_metric" "sign_ins" {
  name    = "${var.prefix}-sign-ins"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"user signed in\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"
  }
}

resource "google_logging_metric" "installation_event" {
  name    = "${var.prefix}-installation-event"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"installation event\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "action"
      value_type  = "STRING"
      description = "Installation action (created/deleted/suspend)"
    }
  }

  label_extractors = {
    "action" = "EXTRACT(jsonPayload.action)"
  }
}

# ---------------------------------------------------------------------------
# Log-based metrics: Token Pool
# ---------------------------------------------------------------------------

resource "google_logging_metric" "token_exhausted" {
  name    = "${var.prefix}-token-exhausted"
  project = var.project_id
  filter  = "resource.type=\"cloud_run_revision\" resource.labels.service_name=\"${var.prefix}-serve\" jsonPayload.msg=\"token exhausted\""

  metric_descriptor {
    metric_kind = "DELTA"
    value_type  = "INT64"

    labels {
      key         = "label"
      value_type  = "STRING"
      description = "Token pool entry label"
    }
  }

  label_extractors = {
    "label" = "EXTRACT(jsonPayload.label)"
  }
}

# ---------------------------------------------------------------------------
# Alert policies
# ---------------------------------------------------------------------------

resource "google_monitoring_alert_policy" "error_rate" {
  display_name          = "${var.prefix}-error-rate"
  project               = var.project_id
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.name]

  conditions {
    display_name = "Cloud Run 5xx error rate"
    condition_threshold {
      filter          = "resource.type = \"cloud_run_revision\" AND resource.labels.service_name = \"${google_cloud_run_v2_service.serve.name}\" AND metric.type = \"run.googleapis.com/request_count\" AND metric.labels.response_code_class = \"5xx\""
      comparison      = "COMPARISON_GT"
      threshold_value = 5
      duration        = "300s"

      aggregations {
        alignment_period   = "60s"
        per_series_aligner = "ALIGN_RATE"
      }
    }
  }
}

resource "google_monitoring_alert_policy" "high_latency" {
  display_name          = "${var.prefix}-high-latency"
  project               = var.project_id
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.name]

  conditions {
    display_name = "Request latency p99 > 2s"
    condition_threshold {
      filter          = "resource.type = \"cloud_run_revision\" AND resource.labels.service_name = \"${google_cloud_run_v2_service.serve.name}\" AND metric.type = \"run.googleapis.com/request_latencies\" AND metric.labels.response_code_class = \"2xx\""
      comparison      = "COMPARISON_GT"
      threshold_value = 2000
      duration        = "600s"

      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_PERCENTILE_99"
      }
    }
  }
}

resource "google_monitoring_alert_policy" "scorer_quota_paused" {
  display_name          = "${var.prefix}-scorer-quota-paused"
  project               = var.project_id
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.name]

  conditions {
    display_name = "Scorer paused due to token quota"
    condition_threshold {
      filter          = "resource.type = \"cloud_run_revision\" AND metric.type = \"logging.googleapis.com/user/${google_logging_metric.scorer_quota_paused.name}\""
      comparison      = "COMPARISON_GT"
      threshold_value = 0
      duration        = "0s"

      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_SUM"
      }
    }
  }
}

resource "google_monitoring_alert_policy" "token_exhaustion" {
  display_name          = "${var.prefix}-token-exhaustion"
  project               = var.project_id
  combiner              = "OR"
  notification_channels = [google_monitoring_notification_channel.email.name]

  conditions {
    display_name = "Token exhaustion > 3 per hour"
    condition_threshold {
      filter          = "resource.type = \"cloud_run_revision\" AND metric.type = \"logging.googleapis.com/user/${google_logging_metric.token_exhausted.name}\""
      comparison      = "COMPARISON_GT"
      threshold_value = 3
      duration        = "0s"

      aggregations {
        alignment_period     = "3600s"
        per_series_aligner   = "ALIGN_SUM"
        cross_series_reducer = "REDUCE_SUM"
      }
    }
  }
}

# ---------------------------------------------------------------------------
# Dashboards
# ---------------------------------------------------------------------------

resource "google_monitoring_dashboard" "service" {
  project        = var.project_id
  dashboard_json = file("${path.module}/dashboard_service.json")
}

resource "google_monitoring_dashboard" "pipeline" {
  project        = var.project_id
  dashboard_json = file("${path.module}/dashboard_pipeline.json")
}
