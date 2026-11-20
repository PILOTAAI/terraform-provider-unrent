# Coding agents' sandboxes go to the servers named sandbox-* (none of which runs anything of production), at most 10
# open at once, 3 per project, each torn down after 4 hours unless extended (never beyond 24 hours).
resource "unrent_sandbox_settings" "this" {
  server_ids  = [for s in data.unrent_servers.all.servers : s.id if startswith(s.name, "sandbox-")]
  max_open    = 10
  per_project = 3
  ttl_hours   = 4
  max_hours   = 24
}
