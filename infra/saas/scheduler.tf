resource "google_service_account" "scheduler" {
  account_id   = "${var.prefix}-scheduler"
  display_name = "DevTrace Cloud Scheduler invoker"
  project      = var.project_id
}

resource "google_cloud_run_v2_job_iam_member" "scheduler_invoke" {
  name     = google_cloud_run_v2_job.ingest.name
  location = var.region
  project  = var.project_id
  role     = "roles/run.invoker"
  member   = "serviceAccount:${google_service_account.scheduler.email}"
}

resource "google_cloud_scheduler_job" "ingest" {
  name      = "${var.prefix}-ingest-hourly"
  schedule  = "20 * * * *"
  time_zone = "Etc/UTC"
  project   = var.project_id
  region    = var.region

  http_target {
    http_method = "POST"
    uri         = "https://${var.region}-run.googleapis.com/apis/run.googleapis.com/v1/namespaces/${var.project_id}/jobs/${google_cloud_run_v2_job.ingest.name}:run"

    oauth_token {
      service_account_email = google_service_account.scheduler.email
      scope                 = "https://www.googleapis.com/auth/cloud-platform"
    }
  }

  retry_config {
    retry_count          = 1
    max_retry_duration   = "0s"
    min_backoff_duration = "5s"
    max_backoff_duration = "5s"
  }

  depends_on = [google_project_service.default]
}
