---
page_title: "unrent_environment Resource - unrent"
subcategory: ""
description: |-
  An environment (its own Docker network on every server, its own variables and approval floor) in a project, with any name.
---

# unrent_environment (Resource)

An environment (its own Docker network on every server, its own variables and approval floor) in a project, with any name. Its `tier` decides its defaults (a `production` tier starts with an approval floor of 1 and waits for an admin's passkey in the console). Environments that already exist (an organisation's first ones, made at setup) are imported by name (`unrent_environments` lists them). Destroying deletes the environment; one that still holds services, databases or domains is refused with their names.

## Schema

### Required

- `name` (String) 

### Optional

- `approvals` (Number) Approver passkeys a deploy, config apply or rollback in this environment needs (0–2; default: the tier's, 1 for `production`, else 0). Servers enforce it too: a change is a policy job approved in the console.
- `clone_from` (String) Create as a clone of this environment (create-time): services, rules as manual, plain variables and secret names; databases empty, crons paused; never domains or protection. Needs developer in its project.
- `colour` (String) The switcher's colour (default: the tier's).
- `description` (String) 
- `label` (String) A display label (`Live`, `EU production`), up to 40 characters. The name stays the environment's id on every server.
- `project_id` (String) The project (default: the organisation's default project). Moving needs admin in both projects.
- `template_id` (String) Create from this template of the project (`etp_…`, `unrent_environment_template`), create-time.
- `tier` (String) `production`, `staging` or `development` (default `development`; the name never decides it). Creating with, or changing to or from, `production` waits for an admin's passkey in the console (the provider prints the page; apply again once it is confirmed).

### Read-Only

- `id` (String) 
- `kind` (String) 
- `network` (String) 
- `protected` (Bool) Whether `unrent_environment_protection` (or the console) protects it.

## Import

```shell
tofu import unrent_environment.example <name>
```
