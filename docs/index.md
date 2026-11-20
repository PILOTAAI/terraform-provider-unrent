---
page_title: "Unrent Provider"
description: |-
  Manage an Unrent organisation through its public API.
---

# Unrent Provider

Manage an Unrent organisation (https://unrent.dev) through its public API. Jobs that need a person's passkey print the approval page and wait for it; secret values are write-only and sealed to the server before they are sent.

Every change needs a token with the `manage` scope (deployments and variable applies also `deploy`). Jobs that need a person's passkey — deleting a service, dropping a database, storage targets, environments with an approval floor — wait for the approval (`approval_timeout`) or fail with its page; the provider never approves anything. Secret values are write-only attributes (Terraform and OpenTofu 1.11 or later), sealed to the server's public key before they are sent: they are never in the plan or the state.

## Example Usage

```terraform
terraform {
  required_version = ">= 1.11" # write-only attributes (OpenTofu 1.11+, Terraform 1.11+)
  required_providers {
    unrent = {
      source  = "pilotaai/unrent"
      version = "~> 0.1"
    }
  }
}

# A personal token with the read, deploy and manage scopes (Settings → API tokens), from UNRENT_TOKEN.
provider "unrent" {
  approval_timeout = "15m" # how long to wait for a passkey approval in the console
}

data "unrent_servers" "web" {
  name = "web-1"
}

resource "unrent_environment" "production" {
  name      = "production" # exists already: tofu import unrent_environment.production production
  approvals = 1
}

resource "unrent_service" "web" {
  environment       = unrent_environment.production.name
  name              = "web"
  server_id         = data.unrent_servers.web.servers[0].id
  image             = "ghcr.io/acme/web:1.4.2"
  port              = 8080
  health_check_path = "/healthz"
}
```

## Schema

### Optional

- `api_url` (String) The API origin (default `https://api.unrent.dev`, or `UNRENT_API`).
- `approval_timeout` (String) How long to wait for a person to approve a held job with a passkey, e.g. `15m` (default). `0s` fails at once with the approval page.
- `job_timeout` (String) How long to wait for a server to finish a job (a deployment, a database, a cron), e.g. `10m` (default).
- `org` (String) The organisation id (`org_…`, or `UNRENT_ORG`). A token belongs to one organisation, so this is only needed for a GitHub Actions exchange; otherwise it is checked against the token's.
- `token` (String, Sensitive) A personal token (`unr_pat_…`) with the `read` and `manage` scopes, or `UNRENT_TOKEN`. Without one the provider uses `github_actions`, then the token of `unrent login`.

### Block `github_actions`

Exchange the running GitHub Actions job's OIDC token for a ten-minute token bound to one environment (the repository must be linked in Settings → CI; the job needs `permissions: id-token: write`). Renewed when it expires during a long apply.

### github_actions Optional

- `environment` (String) The Unrent environment the repository is linked to.

