# Cloud SQL for PostgreSQL.
#
# Satisfies the contract in variables.tf/outputs.tf, which is byte-identical to
# the azure and aws modules. Everything cloud-specific is confined to this file.
#
# Note what is NOT here: TimescaleDB. Cloud SQL does not offer it, and it does not
# need to — migration 000008 detects the extension's absence and builds
# metric_point as a natively partitioned table instead. No application code
# changes. That fallback is why "managed Postgres" is a viable answer at all.

terraform {
  # Pinned so a module cannot be planned by a Terraform old enough to
  # mis-handle it. Same value in every module, on every cloud.
  required_version = ">= 1.10"

  required_providers {
    google = { source = "hashicorp/google", version = "~> 6.0" }
    random = { source = "hashicorp/random", version = "~> 3.6" }
  }
}

locals {
  # The translation layer. The env stack says "medium"; this decides what that
  # means on GCP. Changing clouds re-uses the word, not the machine type.
  sizes = {
    small  = "db-custom-1-3840"
    medium = "db-custom-2-7680"
    large  = "db-custom-4-15360"
  }

  host = google_sql_database_instance.this.private_ip_address
}

resource "random_password" "db" {
  length  = 32
  special = false
}

# trivy:ignore:AVD-GCP-0015 False positive. The rule looks for the DEPRECATED
# `require_ssl` attribute, which provider v6 replaced with `ssl_mode`. This instance sets
# ssl_mode = "ENCRYPTED_ONLY", which is strictly stronger than the old require_ssl: it
# rejects every unencrypted connection at the server. Setting both is not possible — they
# conflict — so the scanner cannot be satisfied without weakening the configuration.
resource "google_sql_database_instance" "this" {
  name             = var.name
  region           = var.region
  database_version = "POSTGRES_${var.postgres_version}"

  # Terraform must be able to delete it, or `terraform destroy` of the old cloud
  # after a migration leaves an instance billing forever.
  deletion_protection = false

  settings {
    tier              = local.sizes[var.instance_size]
    disk_size         = var.storage_gb
    disk_autoresize   = true
    availability_type = var.high_availability ? "REGIONAL" : "ZONAL"

    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = true
      transaction_log_retention_days = 7
      backup_retention_settings {
        retained_backups = var.backup_retention_days
      }
    }

    ip_configuration {
      # No public IP. The VM reaches it over the private network; nothing else
      # reaches it at all.
      ipv4_enabled    = false
      private_network = var.network_id
      ssl_mode        = "ENCRYPTED_ONLY"
    }

    user_labels = var.tags
  }
}

resource "google_sql_database" "this" {
  name     = var.name
  instance = google_sql_database_instance.this.name
}

resource "google_sql_user" "this" {
  name     = var.name
  instance = google_sql_database_instance.this.name
  password = random_password.db.result
}
