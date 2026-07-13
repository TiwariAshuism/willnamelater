# Azure Cache for Redis.
terraform {
  # Pinned so a module cannot be planned by a Terraform old enough to
  # mis-handle it. Same value in every module, on every cloud.
  required_version = ">= 1.10"

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

  # THE CACHE MUST NOT BE ON THE PUBLIC INTERNET.
  #
  # This is the one place the three clouds genuinely diverge, and it is worth naming.
  # Memorystore is reached over PRIVATE_SERVICE_ACCESS and ElastiCache lives in a
  # subnet group — both are private by construction. Azure Cache for Redis is
  # PUBLIC by default, and VNet *injection* is a Premium-only feature, so the obvious
  # translation of "attach it to the network" silently does not exist on the Standard
  # tier this module uses by default.
  #
  # Left alone, that means the Azure cache would have been internet-facing (TLS and an
  # auth key, but reachable) while the other two were not — a divergence that
  # `terraform validate` cannot see, because nothing is syntactically wrong with simply
  # not using var.network_id. tflint caught it as an unused variable, which is what an
  # unused variable in a portability contract usually means: a cloud quietly not doing
  # the thing the other two do.
  #
  # A private endpoint gives a private IP in the VNet on every tier, so the answer does
  # not depend on the SKU.
  public_network_access_enabled = false

  tags = var.tags
}

# The private IP that makes the cache reachable from the VM and from nowhere else.
#
# It is placed on the APP subnet, not on the network module's `id` output — that output
# is the subnet DELEGATED to Flexible Server, and a delegated subnet cannot host a
# private endpoint. compute/azure derives the app subnet the same way, for the same
# reason: the shared contract emits one network id, and Azure needs two subnets.
resource "azurerm_private_endpoint" "redis" {
  name                = "${var.name}-redis-pe"
  location            = var.region
  resource_group_name = var.tags["resource_group"]
  subnet_id           = replace(var.network_id, "-data", "-app")
  tags                = var.tags

  private_service_connection {
    name                           = "${var.name}-redis"
    private_connection_resource_id = azurerm_redis_cache.this.id
    subresource_names              = ["redisCache"]
    is_manual_connection           = false
  }
}
