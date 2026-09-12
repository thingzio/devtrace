# Copyright 2026 Thingz LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# SPDX-License-Identifier: Apache-2.0

resource "google_secret_manager_secret" "github_app_key" {
  secret_id = "${var.prefix}-github-app-key"
  project   = var.project_id

  replication {
    auto {}
  }

  depends_on = [google_project_service.default]
}

resource "google_secret_manager_secret" "oauth_client_secret" {
  secret_id = "${var.prefix}-oauth-client-secret"
  project   = var.project_id

  replication {
    auto {}
  }

  depends_on = [google_project_service.default]
}

resource "google_secret_manager_secret" "webhook_secret" {
  secret_id = "${var.prefix}-webhook-secret"
  project   = var.project_id

  replication {
    auto {}
  }

  depends_on = [google_project_service.default]
}

resource "google_secret_manager_secret" "anthropic_api_key" {
  secret_id = "${var.prefix}-anthropic-api-key"
  project   = var.project_id

  replication {
    auto {}
  }

  depends_on = [google_project_service.default]
}

resource "google_secret_manager_secret" "send_api_key" {
  secret_id = "${var.prefix}-send-api-key"
  project   = var.project_id

  replication {
    auto {}
  }

  depends_on = [google_project_service.default]
}

resource "google_secret_manager_secret" "database_url" {
  secret_id = "${var.prefix}-database-url"
  project   = var.project_id

  replication {
    auto {}
  }

  depends_on = [google_project_service.default]
}

resource "google_secret_manager_secret_version" "database_url" {
  secret      = google_secret_manager_secret.database_url.id
  secret_data = "host=/cloudsql/${local.db_connection} dbname=${var.db_name} user=${google_sql_user.app.name} password=${random_password.db_password.result} sslmode=disable"
}

resource "google_secret_manager_secret" "github_token" {
  count     = var.github_token != "" ? 1 : 0
  secret_id = "${var.prefix}-github-token"
  project   = var.project_id

  replication {
    auto {}
  }

  depends_on = [google_project_service.default]
}

resource "google_secret_manager_secret_version" "github_token" {
  count       = var.github_token != "" ? 1 : 0
  secret      = google_secret_manager_secret.github_token[0].id
  secret_data = var.github_token
}

resource "google_secret_manager_secret" "digest_hmac_secret" {
  count     = var.digest_hmac_secret != "" ? 1 : 0
  secret_id = "${var.prefix}-digest-hmac-secret"
  project   = var.project_id

  replication {
    auto {}
  }

  depends_on = [google_project_service.default]
}

resource "google_secret_manager_secret_version" "digest_hmac_secret" {
  count       = var.digest_hmac_secret != "" ? 1 : 0
  secret      = google_secret_manager_secret.digest_hmac_secret[0].id
  secret_data = var.digest_hmac_secret
}

resource "google_secret_manager_secret_iam_member" "run_digest_hmac" {
  count     = var.digest_hmac_secret != "" ? 1 : 0
  secret_id = google_secret_manager_secret.digest_hmac_secret[0].id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.run.email}"
}

resource "google_secret_manager_secret_iam_member" "run_anthropic" {
  secret_id = google_secret_manager_secret.anthropic_api_key.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.run.email}"
}

resource "google_secret_manager_secret_iam_member" "run_send_api_key" {
  secret_id = google_secret_manager_secret.send_api_key.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.run.email}"
}

resource "google_secret_manager_secret_iam_member" "run_github_app" {
  secret_id = google_secret_manager_secret.github_app_key.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.run.email}"
}

resource "google_secret_manager_secret_iam_member" "run_oauth" {
  secret_id = google_secret_manager_secret.oauth_client_secret.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.run.email}"
}

resource "google_secret_manager_secret_iam_member" "run_webhook" {
  secret_id = google_secret_manager_secret.webhook_secret.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.run.email}"
}

resource "google_secret_manager_secret_iam_member" "run_database_url" {
  secret_id = google_secret_manager_secret.database_url.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.run.email}"
}

resource "google_secret_manager_secret_iam_member" "run_github_token" {
  count     = var.github_token != "" ? 1 : 0
  secret_id = google_secret_manager_secret.github_token[0].id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.run.email}"
}
