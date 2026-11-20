---
page_title: "unrent_branch_environments Resource - unrent"
subcategory: ""
description: |-
  A project's branch-environment rules (all of them): each branch a rule matches gets an ephemeral environment `b-<slug>-<hash>`, created from `from` (an environment of the project or a template), on `server_id`, torn down after `ttl_hours` without a push or when the branch is deleted.
---

# unrent_branch_environments (Resource)

A project's branch-environment rules (all of them): each branch a rule matches gets an ephemeral environment `b-<slug>-<hash>`, created from `from` (an environment of the project or a template), on `server_id`, torn down after `ttl_hours` without a push or when the branch is deleted. Destroying this resource removes the rules and tears their environments down.

## Example Usage

```terraform
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
```

## Schema

### Required

- `project_id` (String) 

### Read-Only

- `id` (String) 

### Block `rule`



### rule Required

- `from` (String) An environment of the project, or a template id (`etp_…`).
- `include` (List[String]) 
- `repo` (String) `github:<repository id>` of a repository the project's services are built from.
- `server_id` (String) 

### rule Optional

- `allow_secrets` (List[String]) Secret names a branch environment may receive (re-sealed with a passkey, never from a production-tier source).
- `exclude` (List[String]) 
- `max_open` (Number) 
- `reset_on_push` (Bool) 
- `ttl_hours` (Number) 

### rule Read-Only

- `id` (String) 

