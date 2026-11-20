---
page_title: "unrent_releases Data Source - unrent"
subcategory: ""
description: |-
  A service's recent releases (newest first), e.g.
---

# unrent_releases (Data Source)

A service's recent releases (newest first), e.g. to pin another environment to the digest that runs in staging.

## Schema

### Required

- `environment` (String) 
- `service` (String) 

### Read-Only

- `live_digest` (String) 
- `releases` (Attributes) 
  - `releases.config_version` 
  - `releases.deployed_at` ms since the epoch
  - `releases.digest` 
  - `releases.live` 
  - `releases.release` 

