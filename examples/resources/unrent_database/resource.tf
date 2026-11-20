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
