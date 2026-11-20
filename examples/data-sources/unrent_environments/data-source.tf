data "unrent_environments" "production" {
  tier = "production"
}

output "production_environments" {
  value = [for e in data.unrent_environments.production.environments : e.name]
}
