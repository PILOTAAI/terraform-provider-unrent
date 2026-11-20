data "unrent_sandboxes" "shop" {
  project_id = unrent_project.shop.id
}

output "sandbox_urls" {
  value = { for s in data.unrent_sandboxes.shop.sandboxes : s.task => [for v in s.services : v.url if v.live] }
}

output "sandbox_servers_without_room" {
  value = [for v in data.unrent_sandboxes.shop.servers : v.name if v.problem != null || v.planned_mb >= coalesce(v.budget_mb, 0)]
}
