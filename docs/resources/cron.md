---
page_title: "unrent_cron Resource - unrent"
subcategory: ""
description: |-
  A schedule: `command` runs in a new container of the service's image, on its server, at `schedule` (UTC).
---

# unrent_cron (Resource)

A schedule: `command` runs in a new container of the service's image, on its server, at `schedule` (UTC). The provider waits for the server to confirm each change.

## Schema

### Required

- `command` (List[String]) The argv (no shell): e.g. `["node", "cleanup.js"]`.
- `environment` (String) 
- `name` (String) 
- `schedule` (String) A 5-field cron expression in UTC, e.g. `0 3 * * *`.
- `service` (String) 

### Optional

- `timeout_s` (Number) 

### Read-Only

- `id` (String) 
- `state` (String) 

## Import

```shell
tofu import unrent_cron.example <environment>/<service>/<name>
```
