# Database and instance are owned by shared infra (thingzio/infra).
# DevTrace creates only its service-specific user.

data "google_sql_database_instance" "shared" {
  name    = var.db_instance_name
  project = var.project_id
}

resource "random_password" "db_password" {
  length  = 32
  special = false
}

resource "google_sql_user" "app" {
  name     = "devtrace"
  instance = data.google_sql_database_instance.shared.name
  password = random_password.db_password.result
}
