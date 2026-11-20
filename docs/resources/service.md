---
page_title: "unrent_service Resource - unrent"
subcategory: ""
description: |-
  A service: a container from a public or private image, or built from a repository (GitHub, GitLab, Bitbucket, Gitea or any SSH Git URL), on one server.
---

# unrent_service (Resource)

A service: a container from a public or private image, or built from a repository (GitHub, GitLab, Bitbucket, Gitea or any SSH Git URL), on one server. Created and deployed in one step. Changing the image redeploys it. Destroying it is a destructive job: the provider prints the approval page and waits for an approver's passkey.

## Example Usage

```terraform
# From an image.
resource "unrent_service" "web" {
  environment       = "production"
  name              = "web"
  server_id         = "agt_0123456789abcdefgh"
  image             = "ghcr.io/acme/web:1.4.2"
  port              = 8080
  health_check_path = "/healthz"
}

# Built from a GitHub repository (the GitHub App installed, and your GitHub account connected in the console).
resource "unrent_service" "api" {
  environment = "production"
  name        = "api"
  server_id   = "agt_0123456789abcdefgh"
  port        = 3000
  repository = {
    type            = "github"
    installation_id = 12345678
    repo_id         = "98765432"
    branch          = "main"
    root_dir        = "services/api"
  }
}
```

## Schema

### Required

- `environment` (String) 
- `name` (String) 
- `port` (Number) 
- `server_id` (String) The server it runs on (`agt_…`, from `data.unrent_servers`).

### Optional

- `health_check_path` (String) An HTTP path that answers 2xx when the service is healthy; without one the health check is a TCP connect.
- `image` (String) An image (`ghcr.io/acme/web:1.2`, or pinned `…@sha256:…`). Changing it deploys the new image. Public images from docker.io, ghcr.io, public.ecr.aws, quay.io or registry.gitlab.com, or any image with `registry_credential_id`.
- `memory_mb` (Number) 
- `registry_credential_id` (String) A registry credential (Settings → Registries) for a private image.
- `repository` (Attributes) Build from a repository instead of an image. The repository and its provider are fixed; branch, directories and auto-deploy can change.
  - `repository.auto_deploy` 
  - `repository.branch` 
  - `repository.connection` gitlab, bitbucket, gitea: the Git connection (Settings → Git providers).
  - `repository.deploy_rule` Which refs deploy the service here (docs/environments-v2.md §4.1). Without it the service follows `branch` and `auto_deploy`. In a protected environment only an admin changes it, inside the protection.
  - `repository.dockerfile` 
  - `repository.installation_id` github: the GitHub App installation id.
  - `repository.repo_id` github, gitlab, bitbucket, gitea: the provider's repository id.
  - `repository.root_dir` 
  - `repository.type` 
  - `repository.url` git: an SSH URL such as `git@git.example.com:team/app.git`.
- `repository_access_token_wo` (String, Sensitive, Write-only: never stored in the plan or state) bitbucket: a repository access token, sealed to the server before it is sent; never stored in the plan or state (write-only, Terraform/OpenTofu ≥ 1.11).
- `wait_for_deployment` (Bool) Wait until a deployment succeeded (default true); a failed one is an error.

### Read-Only

- `id` (String) `<environment>/<name>`
- `live_digest` (String) The image digest that runs now.
- `recipient` (String) The server's public age key (secrets are sealed to it).
- `state` (String) running, stopped, deploying, failed, never_deployed, …

## Import

```shell
tofu import unrent_service.example <environment>/<name>
```
