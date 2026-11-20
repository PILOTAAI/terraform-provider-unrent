---
page_title: "unrent_service_variables Resource - unrent"
subcategory: ""
description: |-
  Variables of a service that this configuration owns: plain values (compared on every refresh) and secrets (write-only: sealed to the service's server before they are sent, never in the plan or the state; bump `secrets_version` to send new values).
---

# unrent_service_variables (Resource)

Variables of a service that this configuration owns: plain values (compared on every refresh) and secrets (write-only: sealed to the service's server before they are sent, never in the plan or the state; bump `secrets_version` to send new values). Variables set elsewhere, such as a database link's, are left alone. With `apply` (default) every change is applied to the running service; in an environment with an approval floor that waits for a passkey.

## Example Usage

```terraform
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
```

## Schema

### Required

- `environment` (String) 
- `service` (String) 

### Optional

- `apply` (Bool) Apply each change to the running service (config.apply), and wait for it.
- `secrets_version` (String) Change it (e.g. a date or a counter) to send `secrets_wo` again. A managed secret deleted elsewhere shows as a change here.
- `secrets_wo` (Map[String], Sensitive, Write-only: never stored in the plan or state) Secret values by name (write-only, Terraform/OpenTofu ≥ 1.11). Sent when the names change or `secrets_version` changes.
- `variables` (Map[String]) Plain values by name.

### Read-Only

- `config_version` (Number) The service's config version after the last change.
- `id` (String) 
- `secret_names` (List[String]) Every secret the service has (names only).

## Import

```shell
tofu import unrent_service_variables.example <environment>/<service>
```
