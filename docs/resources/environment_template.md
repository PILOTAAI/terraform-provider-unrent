---
page_title: "unrent_environment_template Resource - unrent"
subcategory: ""
description: |-
  A definition-only template saved from an environment of a project: services, databases, rules, variable names and plain values, placement hints (never a secret value).
---

# unrent_environment_template (Resource)

A definition-only template saved from an environment of a project: services, databases, rules, variable names and plain values, placement hints (never a secret value). A snapshot: changing `from_environment` or `name` saves a new one.

## Example Usage

```terraform
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
```

## Schema

### Required

- `from_environment` (String) An environment of the project to snapshot.
- `name` (String) 
- `project_id` (String) 

### Read-Only

- `id` (String) `etp_…`
- `spec` (String) The snapshot as JSON.

