resource "google_artifact_registry_repository" "images" {
  location      = var.region
  repository_id = "devtrace-saas-images"
  format        = "DOCKER"
  description   = "DevTrace container images"
}
