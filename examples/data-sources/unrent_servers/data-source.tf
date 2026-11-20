data "unrent_servers" "all" {}

output "connected" {
  value = [for s in data.unrent_servers.all.servers : s.name if s.connected]
}
