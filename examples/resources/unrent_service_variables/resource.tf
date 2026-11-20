variable "stripe_key" {
  type      = string
  sensitive = true
  ephemeral = true # never stored in the plan or the state
}

resource "unrent_service_variables" "web" {
  environment = "production"
  service     = "web"
  variables   = { LOG_LEVEL = "info", FEATURE_X = "on" }
  # Sealed to the service's server before it leaves this machine; bump secrets_version to send new values.
  secrets_wo      = { STRIPE_KEY = var.stripe_key }
  secrets_version = "2026-11-16"
}
