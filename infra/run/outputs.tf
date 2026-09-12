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

output "service_url" {
  description = "Cloud Run service URL"
  value       = google_cloud_run_v2_service.serve.uri
}

output "service_account_email" {
  description = "Runtime service account email"
  value       = google_service_account.run.email
}

output "image_repo" {
  description = "Artifact Registry repository path"
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.images.repository_id}"
}

output "project_id" {
  description = "GCP project ID"
  value       = var.project_id
}

output "ar_repo" {
  description = "Artifact Registry repository ID"
  value       = google_artifact_registry_repository.images.repository_id
}

output "deployer_sa" {
  description = "GitHub Actions deployer service account email"
  value       = google_service_account.deployer.email
}

output "wif_provider" {
  description = "Workload Identity Federation provider for GitHub Actions"
  value       = google_iam_workload_identity_pool_provider.github.name
}
