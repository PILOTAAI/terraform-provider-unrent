---
page_title: "unrent_status_component Resource - unrent"
subcategory: ""
description: |-
  A component of the status page: a service's health, an HTTPS probe of one of the organisation's verified domains or default hostnames, or both.
---

# unrent_status_component (Resource)

A component of the status page: a service's health, an HTTPS probe of one of the organisation's verified domains or default hostnames, or both.

## Schema

### Required

- `name` (String) 

### Optional

- `description` (String) 
- `position` (Number) 
- `probe` (Attributes) 
  - `probe.host` 
  - `probe.path` 
- `service` (Attributes) 
  - `service.environment` 
  - `service.name` 

### Read-Only

- `id` (String) 
- `state` (String) 

## Import

```shell
tofu import unrent_status_component.example cmp_…
```
