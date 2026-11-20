---
page_title: "unrent_database_link Resource - unrent"
subcategory: ""
description: |-
  Links a database to a service on the same server: its connection (the password sealed to that server) becomes the service's variables.
---

# unrent_database_link (Resource)

Links a database to a service on the same server: its connection (the password sealed to that server) becomes the service's variables. With `apply` (default) the service is reconfigured at once.

## Schema

### Required

- `database` (String) 
- `environment` (String) 
- `service` (String) 

### Optional

- `apply` (Bool) 

### Read-Only

- `id` (String) 
- `names` (List[String]) The variables the link set.

## Import

```shell
tofu import unrent_database_link.example <environment>/<database>/<service>
```
