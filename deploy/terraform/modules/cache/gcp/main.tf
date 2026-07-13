# Memorystore for Redis.
terraform {
  required_providers {
    google = { source = "hashicorp/google", version = "~> 6.0" }
  }
}

locals {
  host     = google_redis_instance.this.host
  port     = google_redis_instance.this.port
  password = google_redis_instance.this.auth_string
}

resource "google_redis_instance" "this" {
  name           = var.name
  region         = var.region
  memory_size_gb = var.memory_gb
  tier           = var.high_availability ? "STANDARD_HA" : "BASIC"

  authorized_network = var.network_id
  connect_mode       = "PRIVATE_SERVICE_ACCESS"

  # Both are mandatory for this application: config.Validate refuses to boot prod
  # with redis.tls disabled, and the asynq broker is handed the same TLS config as
  # the cache client so the two cannot diverge.
  auth_enabled            = true
  transit_encryption_mode = "SERVER_AUTHENTICATION"

  labels = var.tags
}
