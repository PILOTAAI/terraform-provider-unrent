---
page_title: "unrent_variables Resource - unrent"
subcategory: ""
description: |-
  A layer of variables this configuration owns: the project's (every environment of the project, a service's own variable wins) or one environment's.
---

# unrent_variables (Resource)

A layer of variables this configuration owns: the project's (every environment of the project, a service's own variable wins) or one environment's. Plain values are compared on every refresh; secrets are write-only, sealed here to each environment's key, never in the plan or the state (bump `secrets_version` to send new values). Values an `unrent.toml` sets are refused (managed_by_file). Each service the layer reaches gets a new config version, applied under its own gates.

## Example Usage

```terraform
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
```

## Schema

### Required

- `scope` (String) 

### Optional

- `environment` (String) scope environment: the environment.
- `project_id` (String) scope project: the project.
- `secrets_version` (String) Change it to send `secrets_wo` again.
- `secrets_wo` (Map[String], Sensitive, Write-only: never stored in the plan or state) Secret values by name (write-only, Terraform/OpenTofu ≥ 1.11): sealed to the environment's key, or to the key of every environment of the project that has one. Sent when the names change or `secrets_version` changes.
- `variables` (Map[String]) Plain values by name.

### Read-Only

- `id` (String) `project/<project_id>` or `environment/<name>`
- `secret_names` (List[String]) Every secret of the layer (names only).

