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
