---
page_title: "unrent_project_role Resource - unrent"
subcategory: ""
description: |-
  A member's role in one project (raises or lowers their org role there).
---

# unrent_project_role (Resource)

A member's role in one project (raises or lowers their org role there). Only for members whose org role is developer or viewer; owners and admins keep theirs everywhere. Needs admin in the project.

## Schema

### Required

- `project_id` (String) 
- `role` (String) 
- `user_id` (String) 

### Read-Only

- `id` (String) `<project_id>/<user_id>`

## Import

```shell
tofu import unrent_project_role.example <project_id>/<user_id>
```
