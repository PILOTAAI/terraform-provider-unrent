---
page_title: "unrent_sandboxes Data Source - unrent"
subcategory: ""
description: |-
  The organisation's open sandboxes (disposable copies of an app that coding agents created for one task) and each sandbox server's budget: half its memory, what its open sandboxes plan, its free memory, and why it takes no new sandbox.
---

# unrent_sandboxes (Data Source)

The organisation's open sandboxes (disposable copies of an app that coding agents created for one task) and each sandbox server's budget: half its memory, what its open sandboxes plan, its free memory, and why it takes no new sandbox. Filter the sandboxes with `project_id`.

## Example Usage

```terraform
data "unrent_sandboxes" "shop" {
  project_id = unrent_project.shop.id
}

output "sandbox_urls" {
  value = { for s in data.unrent_sandboxes.shop.sandboxes : s.task => [for v in s.services : v.url if v.live] }
}

output "sandbox_servers_without_room" {
  value = [for v in data.unrent_sandboxes.shop.servers : v.name if v.problem != null || v.planned_mb >= coalesce(v.budget_mb, 0)]
}
```

## Schema

### Optional

- `project_id` (String) Only the sandboxes of this project.

### Read-Only

- `sandboxes` (Attributes) 
  - `sandboxes.client` The app or token that created it.
  - `sandboxes.environment` 
  - `sandboxes.expires_at` When it is torn down (RFC 3339).
  - `sandboxes.id` 
  - `sandboxes.project_id` 
  - `sandboxes.server_id` 
  - `sandboxes.services` 
  - `sandboxes.source` 
  - `sandboxes.state` `preparing` (its database is being copied) or `open`.
  - `sandboxes.status` `preparing`, `deploying`, `live` or `failed`.
  - `sandboxes.task` 
- `servers` (Attributes) 
  - `servers.budget_mb` Half its memory; null until it reported its memory.
  - `servers.free_mb` 
  - `servers.id` 
  - `servers.name` 
  - `servers.open` 
  - `servers.planned_mb` 
  - `servers.problem` Why it takes no new sandbox (it runs production, or it is gone); null when it does.

