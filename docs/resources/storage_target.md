---
page_title: "unrent_storage_target Resource - unrent"
subcategory: ""
description: |-
  An S3-compatible bucket (AWS, Cloudflare R2, Backblaze B2 or any S3 endpoint) a server backs databases up to.
---

# unrent_storage_target (Resource)

An S3-compatible bucket (AWS, Cloudflare R2, Backblaze B2 or any S3 endpoint) a server backs databases up to. The keys are write-only: sealed to that server before they are sent, never in the plan or the state. Every change is a policy job: the provider prints the approval page and waits for an approver's passkey. The server must encrypt backups first (Settings → Backup encryption).

## Example Usage

```terraform
variable "r2_key_id" {
  type      = string
  ephemeral = true
}
variable "r2_secret" {
  type      = string
  sensitive = true
  ephemeral = true
}

# A policy job: the apply waits until an approver approves it with a passkey in the console.
resource "unrent_storage_target" "r2" {
  server_id            = "agt_0123456789abcdefgh"
  target_id            = "r2"
  storage_provider     = "r2"
  bucket               = "acme-backups"
  endpoint             = "https://<account>.r2.cloudflarestorage.com"
  access_key_id_wo     = var.r2_key_id
  secret_access_key_wo = var.r2_secret
  credentials_version  = "1"
}
```

## Schema

### Required

- `access_key_id_wo` (String, Sensitive, Write-only: never stored in the plan or state) Write-only (Terraform/OpenTofu ≥ 1.11); sealed to the server.
- `bucket` (String) 
- `secret_access_key_wo` (String, Sensitive, Write-only: never stored in the plan or state) Write-only (Terraform/OpenTofu ≥ 1.11); sealed to the server.
- `server_id` (String) 
- `storage_provider` (String) 
- `target_id` (String) The target's name on the server (what `unrent_database.storage_id` refers to).

### Optional

- `credentials_version` (String) Change it to send new keys (any other change sends them too).
- `endpoint` (String) https:// endpoint; required for r2, b2 and s3.
- `force_path_style` (Bool) 
- `prefix` (String) 
- `region` (String) e.g. `us-east-1`; `auto` for R2 (the default there).
- `storage_class` (String) 
- `versioning` (String) 

### Read-Only

- `id` (String) 
- `state` (String) pending_approval, configuring, testing, ok, …

## Import

```shell
tofu import unrent_storage_target.example <server_id>/<target_id>
```
