---
page_title: "unrent_database Resource - unrent"
subcategory: ""
description: |-
  A database (Postgres, MySQL, MariaDB, Valkey, Redis, ClickHouse, Meilisearch, OpenSearch, NATS JetStream, RabbitMQ, MongoDB or DocumentDB (MongoDB-compatible); ClickHouse and OpenSearch need a server with at least 4 GB) on one server with verified backups to a storage target.
---

# unrent_database (Resource)

A database (Postgres, MySQL, MariaDB, Valkey, Redis, ClickHouse, Meilisearch, OpenSearch, NATS JetStream, RabbitMQ, MongoDB or DocumentDB (MongoDB-compatible); ClickHouse and OpenSearch need a server with at least 4 GB) on one server with verified backups to a storage target. Its password is generated on the server and sealed there: only connection names and plain values are visible. **Destroying it drops it**: a destructive job (after a final backup, unless waived) that waits for an approver's passkey; unlink every service first. Version, memory and storage changes are not made through the API (use the console: they are upgrades and verified moves), so the plan refuses them rather than dropping the database.

## Example Usage

```terraform
resource "unrent_database" "shop" {
  environment = "production"
  name        = "shop"
  server_id   = "agt_0123456789abcdefgh"
  engine      = "postgres"
  storage_id  = unrent_storage_target.r2.target_id
}

# The connection (password sealed to the server) becomes the service's variables, applied at once.
resource "unrent_database_link" "shop_web" {
  environment = "production"
  database    = unrent_database.shop.name
  service     = "web"
}
```

## Schema

### Required

- `environment` (String) 
- `name` (String) 
- `server_id` (String) 

### Optional

- `engine` (String) 
- `final_backup` (String) On destroy: `take` a final backup first (default) or `waive` it.
- `keep_days` (Number) On destroy: days the data volume is kept (1–30).
- `memory_mb` (Number) 
- `storage_id` (String) Where backups go: `local` or an `unrent_storage_target` of the same server.
- `version` (String) The engine version (default: the newest Unrent supports).

### Read-Only

- `connection_values` (Map[String]) Plain connection values (host, port, database, user).
- `id` (String) 
- `secret_names` (List[String]) Names of the sealed connection values (the password): link a service to receive them.
- `state` (String) 

## Import

```shell
tofu import unrent_database.example <environment>/<name>
```
