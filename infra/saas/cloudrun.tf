resource "google_cloud_run_v2_service" "serve" {
  name                = "${var.prefix}-serve"
  location            = var.region
  project             = var.project_id
  deletion_protection = false # TODO: set to true after initial deploy

  template {
    service_account = google_service_account.run.email

    scaling {
      min_instance_count = 0
      max_instance_count = 10
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
        name  = "DATABASE_URL"
        value = "host=/cloudsql/${local.db_connection} dbname=${var.db_name} user=${google_sql_user.app.name} password=${random_password.db_password.result} sslmode=disable"
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
        name  = "GITHUB_APP_ID"
        value = var.github_app_id
      }

      env {
        name  = "GITHUB_APP_KEY_PATH"
        value = "/secrets/github-app-key/key.pem"
      }

      dynamic "env" {
        for_each = var.github_token != "" ? [1] : []
        content {
          name  = "GITHUB_TOKEN"
          value = var.github_token
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

resource "google_cloud_run_v2_job" "ingest" {
  name                = "${var.prefix}-ingest"
  location            = var.region
  project             = var.project_id
  deletion_protection = false

  template {
    parallelism = 1
    task_count  = 1

    template {
      timeout         = "3300s"
      service_account = google_service_account.run.email
      max_retries     = 1

      vpc_access {
        network_interfaces {
          network    = local.vpc_id
          subnetwork = local.subnet_id
        }
        egress = "PRIVATE_RANGES_ONLY"
      }

      containers {
        image = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}/devtrace-ingest:${var.image_tag}"

        env {
          name  = "DATABASE_URL"
          value = "host=/cloudsql/${local.db_connection} dbname=${var.db_name} user=${google_sql_user.app.name} password=${random_password.db_password.result} sslmode=disable"
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

        resources {
          limits = {
            cpu    = "1000m"
            memory = "1Gi"
          }
        }

        volume_mounts {
          name       = "cloudsql"
          mount_path = "/cloudsql"
        }
      }

      volumes {
        name = "cloudsql"
        cloud_sql_instance {
          instances = [local.db_connection]
        }
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
