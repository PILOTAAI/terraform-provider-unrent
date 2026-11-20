---
page_title: "unrent_domain Resource - unrent"
subcategory: ""
description: |-
  A custom domain routed to a service (`*.<zone>` for a wildcard; `www = true` also routes `www.<apex>`).
---

# unrent_domain (Resource)

A custom domain routed to a service (`*.<zone>` for a wildcard; `www = true` also routes `www.<apex>`). It serves with TLS once its DNS verifies: create the `records` (e.g. with your DNS provider's Terraform resources), or let a DNS connection do it.

## Schema

### Required

- `environment` (String) 
- `host` (String) 
- `service` (String) 

### Optional

- `www` (Bool) 

### Read-Only

- `id` (String) 
- `records` (List[Object["name":String, "type":String, "value":String]]) DNS records to create: the ownership TXT record, then A/AAAA (or a CNAME alternative).
- `state` (String) pending, verified, active, …

## Import

```shell
tofu import unrent_domain.example <host>
```
