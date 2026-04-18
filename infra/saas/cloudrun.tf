resource "google_cloud_run_v2_service" "serve" {
  name                = "${var.prefix}-serve"
  location            = var.region
  project             = var.project_id
  deletion_protection = false # TODO: set to true after initial deploy

  template {
    service_account = google_service_account.run.email

    scaling {
      min_instance_count = 1
      max_instance_count = 1
    }

    vpc_access {
      network_interfaces {
        network    = local.vpc_id
        subnetwork = local.subnet_id
      }
      egress = "PRIVATE_RANGES_ONLY"
    }

    containers {
      image = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}/devtrace-site:${var.image_tag}"

      ports {
        container_port = 8080
      }

      env {
        name = "DATABASE_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.database_url.secret_id
            version = "latest"
          }
        }
      }

      env {
        name  = "GITHUB_OAUTH_CLIENT_ID"
        value = var.github_oauth_client_id
      }

      env {
        name = "GITHUB_OAUTH_CLIENT_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.oauth_client_secret.secret_id
            version = "latest"
          }
        }
      }

      env {
        name = "GITHUB_WEBHOOK_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.webhook_secret.secret_id
            version = "latest"
          }
        }
      }

      env {
        name = "ANTHROPIC_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.anthropic_api_key.secret_id
            version = "latest"
          }
        }
      }

      env {
        name  = "BASE_URL"
        value = "https://${var.domain}"
      }

      env {
        name  = "TRUST_PROXY"
        value = "true"
      }

      env {
        name  = "ENABLE_BACKGROUND_OPS"
        value = "true"
      }

      env {
        name  = "GITHUB_APP_ID"
        value = var.github_app_id
      }

      env {
        name  = "GITHUB_APP_KEY_PATH"
        value = "/secrets/github-app-key/key.pem"
      }

      env {
        name  = "DEVTRACE_ADMIN_USERS"
        value = var.admin_users
      }

      env {
        name = "SEND_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.send_api_key.secret_id
            version = "latest"
          }
        }
      }

      env {
        name  = "SUPPORT_EMAIL"
        value = "devtrace@thingz.io"
      }

      dynamic "env" {
        for_each = var.github_token != "" ? [1] : []
        content {
          name = "GITHUB_TOKEN"
          value_source {
            secret_key_ref {
              secret  = google_secret_manager_secret.github_token[0].secret_id
              version = "latest"
            }
          }
        }
      }

      resources {
        limits = {
          cpu    = "1000m"
          memory = "512Mi"
        }
      }

      volume_mounts {
        name       = "github-app-key"
        mount_path = "/secrets/github-app-key"
      }

      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }

      startup_probe {
        http_get {
          path = "/health"
        }
        initial_delay_seconds = 2
        period_seconds        = 3
        failure_threshold     = 5
      }
    }

    volumes {
      name = "github-app-key"
      secret {
        secret = google_secret_manager_secret.github_app_key.secret_id
        items {
          version = "latest"
          path    = "key.pem"
        }
      }
    }

    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [local.db_connection]
      }
    }
  }

  depends_on = [google_project_service.default]
}


resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.serve.name
  location = var.region
  project  = var.project_id
  role     = "roles/run.invoker"
  member   = "allUsers"
}
