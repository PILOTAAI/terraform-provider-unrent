---
page_title: "unrent_project Resource - unrent"
subcategory: ""
description: |-
  A project groups environments and carries per-project roles.
---

# unrent_project (Resource)

A project groups environments and carries per-project roles. Creating and archiving need an org admin. `terraform destroy` **archives** the project (Unrent keeps it for its audit trail); restore it in the console or import it again.

## Schema

### Required

- `name` (String) 1–64 characters, unique in the organisation.

### Read-Only

- `environments` (List[String]) Environments in the project (manage them with `unrent_environment.project_id`).
- `id` (String) `prj_…`

## Import

```shell
tofu import unrent_project.example prj_…
```
