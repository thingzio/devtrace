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

locals {
  # Shared infra references (from variables, not remote state)
  vpc_id        = var.vpc_id
  subnet_id     = var.subnet_id
  db_connection = var.db_connection_name

  # Service-specific APIs (shared infra already enables compute, sqladmin,
  # servicenetworking, monitoring, iam, and others).
  services = [
    "artifactregistry.googleapis.com",
    "run.googleapis.com",
    "secretmanager.googleapis.com",
    "monitoring.googleapis.com",
    "iam.googleapis.com",
    "cloudscheduler.googleapis.com",
  ]
}

resource "google_project_service" "default" {
  for_each = toset(local.services)
  project  = var.project_id
  service  = each.value
}

data "google_project" "default" {
  project_id = var.project_id
}
