---
page_title: "unrent_sandbox_settings Resource - unrent"
subcategory: ""
description: |-
  The organisation's sandbox settings (one per organisation): the sandbox servers coding agents' sandboxes go to, and the caps.
---

# unrent_sandbox_settings (Resource)

The organisation's sandbox settings (one per organisation): the sandbox servers coding agents' sandboxes go to, and the caps. A sandbox with a database copy goes to the server holding that database; any other to the server with the most budget (half its memory) left. No sandbox server may run anything of a production-tier environment: the API refuses one that does, naming it. Destroying this resource turns sandboxes off (`max_open` 0, no server); open sandboxes run until their time to live. Needs an org admin and the `manage` scope. There is no resource for a single sandbox (its time to live would be drift): read them with the `unrent_sandboxes` data source.

## Example Usage

```terraform
# Coding agents' sandboxes go to the servers named sandbox-* (none of which runs anything of production), at most 10
# open at once, 3 per project, each torn down after 4 hours unless extended (never beyond 24 hours).
resource "unrent_sandbox_settings" "this" {
  server_ids  = [for s in data.unrent_servers.all.servers : s.id if startswith(s.name, "sandbox-")]
  max_open    = 10
  per_project = 3
  ttl_hours   = 4
  max_hours   = 24
}
```

## Schema

### Required

- `server_ids` (Set[String]) The sandbox servers (at most 20; empty for none). See the `unrent_servers` data source.

### Optional

- `max_hours` (Number) The longest life of a sandbox, from its creation. Default 72.
- `max_open` (Number) Open sandboxes in the organisation (0 turns them off). Default 5.
- `per_project` (Number) Open sandboxes per project. Default 3.
- `ttl_hours` (Number) The default time to live, at most `max_hours`. Default 4.

### Read-Only

- `id` (String) 

