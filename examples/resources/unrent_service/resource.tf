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
