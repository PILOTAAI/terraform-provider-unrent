# Every feature/** branch of the repository gets its own environment, cloned from staging, for 72 hours.
resource "unrent_branch_environments" "shop" {
  project_id = unrent_project.shop.id

  rule {
    repo      = "github:123456789"
    include   = ["feature/**"]
    exclude   = ["feature/wip-*"]
    from      = "staging"
    server_id = data.unrent_servers.all.servers[0].id
    ttl_hours = 72
    max_open  = 5
  }
}
