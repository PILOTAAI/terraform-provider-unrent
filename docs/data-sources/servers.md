---
page_title: "unrent_servers Data Source - unrent"
subcategory: ""
description: |-
  Servers enrolled in the organisation (enrolling runs the installer on the server, not Terraform).
---

# unrent_servers (Data Source)

Servers enrolled in the organisation (enrolling runs the installer on the server, not Terraform). Filter by hostname with `name`.

## Example Usage

```terraform
data "unrent_servers" "all" {}

output "connected" {
  value = [for s in data.unrent_servers.all.servers : s.name if s.connected]
}
```

## Schema

### Optional

- `name` (String) Only the server with this hostname.

### Read-Only

- `servers` (Attributes) 
  - `servers.arch` 
  - `servers.connected` 
  - `servers.id` 
  - `servers.mode` 
  - `servers.name` 
  - `servers.os` 
  - `servers.version` 

