variable "project_id" {
  description = "GCP project ID for the SaaS deployment"
  type        = string
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
}

variable "git_repo" {
  description = "GitHub repository for federated identity"
  type        = string
}

variable "github_oauth_client_id" {
  description = "GitHub OAuth App client ID (public, not a secret)"
  type        = string
}

variable "github_app_id" {
  description = "GitHub App ID for installation token minting"
  type        = string
}

variable "admin_invoker_emails" {
  description = "GCP identities allowed to invoke the admin service"
  type        = list(string)
}

variable "admin_users" {
  description = "Comma-separated GitHub usernames for admin dashboard access"
  type        = string
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
}

variable "subnet_id" {
  description = "Shared VPC subnet ID"
  type        = string
}

variable "db_instance_name" {
  description = "Shared Cloud SQL instance name"
  type        = string
}

variable "db_connection_name" {
  description = "Shared Cloud SQL connection string (project:region:instance)"
  type        = string
}

variable "db_name" {
  description = "Database name within the shared Cloud SQL instance"
  type        = string
}

variable "notification_email" {
  description = "Email address for alert notifications"
  type        = string
}

variable "digest_dry_run" {
  description = "When true, weekly digest emails are only sent to admin users. Set to false to enable for all eligible tenants."
  type        = bool
  default     = true
}

variable "digest_hmac_secret" {
  description = "HMAC-SHA256 secret for signing one-click unsubscribe tokens in digest emails"
  type        = string
  sensitive   = true
}

variable "github_token" {
  description = "GitHub PAT fallback for API calls (optional, used during bootstrap)"
  type        = string
  sensitive   = true
}
