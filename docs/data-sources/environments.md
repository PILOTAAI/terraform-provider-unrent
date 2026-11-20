---
page_title: "unrent_environments Data Source - unrent"
subcategory: ""
description: |-
  The organisation's environments, production-tier first.
---

# unrent_environments (Data Source)

The organisation's environments, production-tier first. Filter with `tier` (e.g. the production-tier environment, whatever its name). Import one into `unrent_environment` by its name.

## Example Usage

```terraform
data "unrent_environments" "production" {
  tier = "production"
}

output "production_environments" {
  value = [for e in data.unrent_environments.production.environments : e.name]
}
```

## Schema

### Optional

- `tier` (String) Only environments of this tier: `production`, `staging`, `development`, `preview` or `ephemeral`.

### Read-Only

- `environments` (Attributes) 
  - `environments.approvals` 
  - `environments.label` 
  - `environments.name` 
  - `environments.network` 
  - `environments.project_id` 
  - `environments.protected` 
  - `environments.tier` 

