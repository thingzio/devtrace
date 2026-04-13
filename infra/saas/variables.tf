variable "project_id" {
  type    = string
  default = "devpulseio"
}

variable "region" {
  type    = string
  default = "us-west1"
}

variable "cloud_sql_instance_name" {
  type        = string
  description = "Shared Cloud SQL instance name"
}

variable "db_password" {
  type      = string
  sensitive = true
}

variable "domain" {
  type    = string
  default = "devtrace.thingz.io"
}

variable "image_tag" {
  type        = string
  description = "Container image tag to deploy"
  default     = "latest"
}
