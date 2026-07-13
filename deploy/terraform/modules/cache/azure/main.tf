# Azure Cache for Redis.
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.0" }
  }
}

locals {
  host     = azurerm_redis_cache.this.hostname
  port     = azurerm_redis_cache.this.ssl_port # 6380. The non-TLS port is disabled below.
  password = azurerm_redis_cache.this.primary_access_key
}

resource "azurerm_redis_cache" "this" {
  name                = var.name
  location            = var.region
  resource_group_name = var.tags["resource_group"]

  capacity = var.memory_gb
  family   = var.high_availability ? "P" : "C"
  sku_name = var.high_availability ? "Premium" : "Standard"

  # Azure is the clearest case of why the Go client needed TLS support: this is the
  # default, and turning it off is not a supported way to run a production cache.
  non_ssl_port_enabled = false
  minimum_tls_version  = "1.2"

  tags = var.tags
}
