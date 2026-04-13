terraform {
  required_version = ">= 1.5"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
  backend "gcs" {
    bucket = "devpulseio-terraform"
    prefix = "devtrace-saas"
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}

# ---------------------------------------------------------------------------
# Shared resources — reference only, DO NOT recreate
# ---------------------------------------------------------------------------

data "google_compute_network" "vpc" {
  name = "devpulse-saas-vpc"
}

data "google_compute_subnetwork" "subnet" {
  name   = "devpulse-saas-subnet"
  region = var.region
}

data "google_sql_database_instance" "db" {
  name = var.cloud_sql_instance_name
}

# ---------------------------------------------------------------------------
# DevTrace-owned database within shared Cloud SQL instance
# ---------------------------------------------------------------------------

resource "google_sql_database" "devtrace" {
  name     = "devtrace"
  instance = data.google_sql_database_instance.db.name
}

resource "google_sql_user" "devtrace" {
  name     = "devtrace"
  instance = data.google_sql_database_instance.db.name
  password = var.db_password
}
