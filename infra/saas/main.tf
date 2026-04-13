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
