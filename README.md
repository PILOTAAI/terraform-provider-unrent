# terraform-provider-unrent

The [Unrent](https://unrent.dev) provider for [OpenTofu](https://opentofu.org) and Terraform: projects,
environments, services (from an image or a repository), variables with sealed secrets, schedules, databases and
their links, domains, storage targets, notification channels, the status page and project roles, managed through
Unrent's public API (`https://api.unrent.dev/v1`) only.

```terraform
terraform {
  required_providers {
    unrent = { source = "pilotaai/unrent" }
  }
}
provider "unrent" {} # UNRENT_TOKEN: a personal token with the read, deploy and manage scopes
```

- **Passkeys are never bypassed.** Deleting a service, dropping a database, changing a storage target and any change
  in an environment with an approval floor wait for a person to approve the exact job with a passkey in the console.
  The provider waits up to `approval_timeout` (default 15 minutes; the job is listed under Approvals in the console)
  or fails with the approval page and the digest the passkey signs.
- **Secrets never reach the state.** Secret values are write-only attributes (`*_wo`, OpenTofu/Terraform 1.11+),
  sealed with [age](https://age-encryption.org) to the server's public key on your machine before they are sent; the
  API never returns them. Bump the matching `*_version` attribute to send new values.
- **GitHub Actions without a stored secret:** `github_actions { environment = "production" }` exchanges the job's
  OIDC token for a ten-minute token bound to that environment.

Documentation: [docs/](docs/) (also on the registries). The source is developed in
[PILOTAAI/unrent](https://github.com/PILOTAAI/unrent) (`terraform-provider-unrent/`, `docs/terraform.md`) and copied
here for each release by `.github/workflows/sync.yml`; change it there.

License: [MIT](LICENSE).
