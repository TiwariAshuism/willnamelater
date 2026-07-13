# Azure Database for PostgreSQL — Flexible Server.
#
# Satisfies the SAME contract as the gcp and aws modules: variables.tf and
# outputs.tf here are byte-identical to theirs. This file is the only thing in the
# module that knows the word "Azure".
#
# Flexible Server DOES offer TimescaleDB — and it is deliberately NOT enabled.
# The application does not require it (migration 000008 falls back to native
# partitioning), so turning it on would re-introduce exactly the lock-in this
# architecture exists to avoid, in exchange for nothing the product uses today.

terraform {
  # Pinned so a module cannot be planned by a Terraform old enough to
  # mis-handle it. Same value in every module, on every cloud.
  required_version = ">= 1.10"

  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.0" }
    random  = { source = "hashicorp/random", version = "~> 3.6" }
  }
}

locals {
  sizes = {
    small  = "GP_Standard_D2s_v3"
    medium = "GP_Standard_D2ds_v4"
    large  = "GP_Standard_D4ds_v4"
  }

  # Azure does not sell storage by the gigabyte. Flexible Server accepts only these
  # fixed tiers, and rejects anything else outright at apply time.
  #
  # THIS IS THE CONTRACT DOING ITS JOB. The env stack passes storage_gb = 100 — the same
  # value, on every cloud — because a portability contract whose values have to be
  # re-chosen per cloud is not a contract. 100 GB is perfectly legal on Cloud SQL and on
  # RDS. On Azure it is not a purchasable thing at all, and `terraform validate` cannot
  # see that: the type is right, the reference resolves, and it fails only when the API
  # is asked for it.
  #
  # So the Azure module absorbs the difference, which is precisely what a cloud-specific
  # main.tf is FOR. Round UP to the smallest tier that satisfies the request: rounding
  # down would quietly give a deployment less storage than it asked for.
  storage_tiers_mb = [32768, 65536, 131072, 262144, 524288, 1048576, 2097152, 4194304, 8388608, 16777216, 33553408]
  requested_mb     = var.storage_gb * 1024
  storage_mb       = coalesce([for t in local.storage_tiers_mb : t if t >= local.requested_mb]...)

  host = azurerm_postgresql_flexible_server.this.fqdn
}

resource "random_password" "db" {
  length  = 32
  special = false
}

resource "azurerm_postgresql_flexible_server" "this" {
  name                = var.name
  resource_group_name = var.tags["resource_group"]
  location            = var.region
  version             = var.postgres_version

  sku_name = local.sizes[var.instance_size]
  # Rounded UP to a purchasable Azure tier. See locals above -- storage_gb = 100 is legal
  # on GCP and AWS and does not exist on Azure.
  storage_mb = local.storage_mb

  administrator_login    = var.name
  administrator_password = random_password.db.result

  backup_retention_days        = var.backup_retention_days
  geo_redundant_backup_enabled = var.high_availability

  # Private access: the server is injected into the VNet's delegated subnet, so it
  # has no public endpoint.
  delegated_subnet_id = var.network_id

  dynamic "high_availability" {
    for_each = var.high_availability ? [1] : []
    content {
      mode = "ZoneRedundant"
    }
  }

  tags = var.tags
}

resource "azurerm_postgresql_flexible_server_database" "this" {
  name      = var.name
  server_id = azurerm_postgresql_flexible_server.this.id
  charset   = "UTF8"
  collation = "en_US.utf8"
}
