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
