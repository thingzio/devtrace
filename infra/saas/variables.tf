variable "project_id" {
  description = "GCP project ID for the SaaS deployment"
  type        = string
  default     = "thingzio"
}

variable "region" {
  description = "GCP region for Cloud Run and Cloud SQL"
  type        = string
  default     = "us-west1"
}

variable "prefix" {
  description = "Unique deployment identifier"
  type        = string
  default     = "devtrace-saas"
}

variable "domain" {
  description = "Public domain for the SaaS service"
  type        = string
  default     = "devtrace.thingz.io"
}

variable "git_repo" {
  description = "GitHub repository for federated identity"
  type        = string
  default     = "thingzio/devtrace"
}

variable "github_oauth_client_id" {
  description = "GitHub OAuth App client ID (public, not a secret)"
  type        = string
  default     = ""
}

variable "github_app_id" {
  description = "GitHub App ID for installation token minting"
  type        = string
  default     = ""
}

variable "admin_invoker_emails" {
  description = "GCP identities allowed to invoke the admin service"
  type        = list(string)
  default     = ["mark@chmarny.com"]
}

variable "admin_users" {
  description = "Comma-separated GitHub usernames for admin dashboard access"
  type        = string
  default     = "mchmarny"
}

variable "image_tag" {
  description = "Container image tag to deploy"
  type        = string
  default     = "latest"
}

# --- Shared infrastructure (from thingzio/infra) ---

variable "vpc_id" {
  description = "Shared VPC network ID"
  type        = string
  default     = "projects/thingzio/global/networks/thingzio-vpc"
}

variable "subnet_id" {
  description = "Shared VPC subnet ID"
  type        = string
  default     = "projects/thingzio/regions/us-west1/subnetworks/thingzio-subnet"
}

variable "db_instance_name" {
  description = "Shared Cloud SQL instance name"
  type        = string
  default     = "thingzio-pg"
}

variable "db_connection_name" {
  description = "Shared Cloud SQL connection string (project:region:instance)"
  type        = string
  default     = "thingzio:us-west1:thingzio-pg"
}

variable "db_name" {
  description = "Database name within the shared Cloud SQL instance"
  type        = string
  default     = "thingz"
}

variable "notification_email" {
  description = "Email address for alert notifications"
  type        = string
  default     = "devtrace@thingz.io"
}

variable "digest_dry_run" {
  description = "When true, weekly digest emails are only sent to admin users. Set to false to enable for all eligible tenants."
  type        = bool
  default     = true
}

variable "digest_hmac_secret" {
  description = "HMAC-SHA256 secret for signing one-click unsubscribe tokens in digest emails"
  type        = string
  default     = ""
  sensitive   = true
}

variable "github_token" {
  description = "GitHub PAT fallback for API calls (optional, used during bootstrap)"
  type        = string
  default     = ""
  sensitive   = true
}
