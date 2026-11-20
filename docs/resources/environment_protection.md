---
page_title: "unrent_environment_protection Resource - unrent"
subcategory: ""
description: |-
  Which code may reach an environment (docs/environments-v2.md §5): allowed branches and tags, repositories, images not built from an allowed ref, promotions, the checks gate, a wait timer, freeze windows and break-glass.
---

# unrent_environment_protection (Resource)

Which code may reach an environment (docs/environments-v2.md §5): allowed branches and tags, repositories, images not built from an allowed ref, promotions, the checks gate, a wait timer, freeze windows and break-glass. Every server of the environment enforces it too. Saving it, and destroying it (protection off), are policy jobs: the provider prints the approval page and waits for approver passkeys (approval_timeout).

## Example Usage

```terraform
# Only commits on main (or a v1.x tag) reach production; every server checks it against the git host too.
# Apply prints the approval page: an approver signs the policy job with a passkey, then apply finishes.
resource "unrent_environment_protection" "production" {
  environment        = "production"
  branches           = ["main"]
  tags               = ["v*"]
  semver             = ">=1.0.0 <2"
  images             = "approval"
  promote_from       = ["staging"]
  wait_timer_minutes = 10

  freeze {
    start_cron = "0 18 * * 5"
    end_cron   = "0 6 * * 1"
    timezone   = "Europe/Berlin"
  }
}
```

## Schema

### Required

- `environment` (String) 

### Optional

- `branches` (List[String]) Branches (globs) a deploy may come from, e.g. `["main"]`.
- `bypass_approvals` (Number) Break-glass: distinct approver passkeys a deploy outside the protection needs (0: none possible).
- `checks` (String) `required`: wait for the commit's GitHub check suites.
- `images` (String) Deploys of an image not built from an allowed ref: `allow`, `approval` (one approver passkey) or `deny`.
- `prevent_self_approval` (Bool) The requester's own passkey does not count.
- `promote_from` (List[String]) Environments a promotion may come from (the commit must also be on an allowed ref).
- `semver` (String) A SemVer range the tag (after its pattern's prefix) must satisfy, e.g. `>=1.0.0 <2`.
- `tags` (List[String]) Tags (globs), e.g. `["v*"]`.
- `wait_timer_minutes` (Number) Minutes a deploy waits before it starts (0–43,200).

### Read-Only

- `id` (String) 

### Block `freeze`

A freeze window from each `start_cron` minute to the next `end_cron` minute in `timezone` (IANA); deploys wait, rollbacks stay possible.

### freeze Required

- `end_cron` (String) 
- `start_cron` (String) 

### freeze Optional

- `timezone` (String) 

