resource "unrent_environment_template" "base" {
  project_id       = unrent_project.shop.id
  name             = "base"
  from_environment = "staging"
}

# An environment made from it (create-time).
resource "unrent_environment" "qa" {
  name        = "qa"
  project_id  = unrent_project.shop.id
  template_id = unrent_environment_template.base.id
  tier        = "development"
  description = "QA"
}
