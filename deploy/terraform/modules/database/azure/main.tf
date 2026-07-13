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

  sku_name   = local.sizes[var.instance_size]
  storage_mb = var.storage_gb * 1024

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
