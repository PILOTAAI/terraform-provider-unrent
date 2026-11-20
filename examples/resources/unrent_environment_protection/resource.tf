# Only commits on main (or a v1.x tag) reach production; every server checks it against the git host too.
# Apply prints the approval page: an approver signs the policy job with a passkey, then apply finishes.
resource "unrent_environment_protection" "production" {
  environment        = "production"
  branches           = ["main"]
  tags               = ["v*"]
  semver             = ">=1.0.0 <2"
  images             = "approval"
  promote_from       = ["staging"]
  wait_timer_minutes = 10

  freeze {
    start_cron = "0 18 * * 5"
    end_cron   = "0 6 * * 1"
    timezone   = "Europe/Berlin"
  }
}
