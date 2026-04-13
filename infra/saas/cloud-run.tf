# ---------------------------------------------------------------------------
# Secrets — resources only; values managed outside Terraform
# ---------------------------------------------------------------------------

resource "google_secret_manager_secret" "db_url" {
  secret_id = "devtrace-saas-db-url"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret" "github_token" {
  secret_id = "devtrace-saas-github-token"
  replication {
    auto {}
  }
}

# ---------------------------------------------------------------------------
# VPC access connector for Cloud Run → private network
# ---------------------------------------------------------------------------

resource "google_vpc_access_connector" "devtrace" {
  name          = "devtrace-saas-vpc"
  region        = var.region
  network       = data.google_compute_network.vpc.id
  ip_cidr_range = "10.8.1.0/28"
}

# ---------------------------------------------------------------------------
# Cloud Run service
# ---------------------------------------------------------------------------

resource "google_cloud_run_v2_service" "serve" {
  name     = "devtrace-saas-serve"
  location = var.region

  template {
    service_account = google_service_account.run.email

    vpc_access {
      connector = google_vpc_access_connector.devtrace.id
      egress    = "PRIVATE_RANGES_ONLY"
    }

    containers {
      image = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}/devtrace-site:${var.image_tag}"

      env {
        name = "DB_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.db_url.secret_id
            version = "latest"
          }
        }
      }

      env {
        name = "GITHUB_TOKEN"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.github_token.secret_id
            version = "latest"
          }
        }
      }

      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      startup_probe {
        http_get {
          path = "/health"
        }
        initial_delay_seconds = 5
        period_seconds        = 10
        failure_threshold     = 3
      }

      liveness_probe {
        http_get {
          path = "/health"
        }
        period_seconds = 30
      }
    }

    scaling {
      min_instance_count = 0
      max_instance_count = 3
    }
  }

  traffic {
    type    = "TRAFFIC_TARGET_ALLOCATION_TYPE_LATEST"
    percent = 100
  }
}
