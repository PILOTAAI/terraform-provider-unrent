variable "slack_webhook" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "unrent_notification_channel" "ops" {
  type             = "slack"
  name             = "#ops"
  min_severity     = "critical"
  settings_wo      = { url = var.slack_webhook }
  settings_version = "1" # change to replace the channel with new settings
}
