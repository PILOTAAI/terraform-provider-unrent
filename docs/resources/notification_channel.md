---
page_title: "unrent_notification_channel Resource - unrent"
subcategory: ""
description: |-
  A channel every alert of the organisation goes to: Slack, Discord, Microsoft Teams, Telegram, ntfy, Pushover or PagerDuty.
---

# unrent_notification_channel (Resource)

A channel every alert of the organisation goes to: Slack, Discord, Microsoft Teams, Telegram, ntfy, Pushover or PagerDuty. Its settings (webhook URLs, tokens) are write-only: sent once, checked and sealed by Unrent, never returned, never in the plan or state. Changing `settings_version` replaces the channel with the new settings. Needs an org admin.

## Example Usage

```terraform
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
```

## Schema

### Required

- `settings_wo` (Map[String], Sensitive, Write-only: never stored in the plan or state) slack, discord, teams: `url`; ntfy: `url` and optional `token`; telegram: `bot_token`, `chat_id`; pushover: `token`, `user`; pagerduty: `routing_key`.
- `type` (String) 

### Optional

- `enabled` (Bool) 
- `min_severity` (String) 
- `name` (String) 
- `settings_version` (String) Change it to replace the channel with new `settings_wo`.

### Read-Only

- `display` (String) A masked hint of the destination.
- `id` (String) 

## Import

```shell
tofu import unrent_notification_channel.example chn_…
```
