variable "sentry_dsn" {
  type      = string
  sensitive = true
  ephemeral = true # never stored in the plan or the state
}

# Every environment of the project (a service's own variable wins).
resource "unrent_variables" "shop" {
  scope      = "project"
  project_id = unrent_project.shop.id
  variables  = { LOG_FORMAT = "json" }
  # Sealed here to the key of each environment of the project that has one.
  secrets_wo      = { SENTRY_DSN = var.sentry_dsn }
  secrets_version = "2026-11-16"
}

# One environment's layer.
resource "unrent_variables" "staging" {
  scope       = "environment"
  environment = "staging"
  variables   = { FEATURE_FLAGS = "beta" }
}
