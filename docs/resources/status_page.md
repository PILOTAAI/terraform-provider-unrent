---
page_title: "unrent_status_page Resource - unrent"
subcategory: ""
description: |-
  The organisation's public status page (one per organisation) at `https://status-<slug>.unrent.page`.
---

# unrent_status_page (Resource)

The organisation's public status page (one per organisation) at `https://status-<slug>.unrent.page`. Destroying it unpublishes it. Needs an org admin.

## Schema

### Required

- `slug` (String) 
- `title` (String) 

### Optional

- `description` (String) 
- `enabled` (Bool) Published (default true).

### Read-Only

- `id` (String) 
- `url` (String) 

## Import

```shell
tofu import unrent_status_page.example status-page
```
