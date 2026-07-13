# Provider-mocked end-to-end test. No cloud credentials, no emulator, no network.
#
# `terraform validate` proves the HCL parses. tflint proves the arguments exist against
# the real provider schema. NEITHER executes the module -- neither would notice that an
# output interpolates a value nothing sets, that a variable validation rule rejects what
# the env stack passes, or that a value legal on one cloud is not purchasable on another.
#
# That last one is not hypothetical. This test is how storage_gb = 100 was caught: legal
# on Cloud SQL and RDS, and rejected outright by Azure Flexible Server, which sells only
# fixed storage tiers. terraform validate saw nothing wrong with it.
#
# mock_provider synthesises computed attributes, so `terraform test` runs a real plan AND
# apply against a fake cloud -- identically for GCP, Azure and AWS.
#
# The override_* blocks below supply values the mock generator cannot invent: a provider
# that validates the FORMAT of an id (GCP network paths) or the CONTENT of a list (AWS
# availability zones) will reject a random string. They stub inputs, not behaviour.

mock_provider "azurerm" {}
mock_provider "cloudflare" {}
mock_provider "random" {}

# azurerm parses ids STRUCTURALLY -- a subnet id must have ten segments, a cache id must
# be a well-formed ARM resource path. The mock generator produces random strings, which
# the provider rejects before it ever reaches a fake API. These stub the shape, not the
# behaviour.
override_resource {
  target = module.network.azurerm_subnet.data
  values = {
    id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/influaudit-prod/providers/Microsoft.Network/virtualNetworks/influaudit-prod/subnets/influaudit-prod-data"
  }
}

override_resource {
  target = module.network.azurerm_subnet.app
  values = {
    id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/influaudit-prod/providers/Microsoft.Network/virtualNetworks/influaudit-prod/subnets/influaudit-prod-app"
  }
}

override_resource {
  target = module.cache.azurerm_redis_cache.this
  values = {
    id                 = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/influaudit-prod/providers/Microsoft.Cache/redis/influaudit-prod"
    hostname           = "influaudit-prod.redis.cache.windows.net"
    ssl_port           = 6380
    primary_access_key = "mock-access-key"
  }
}

override_resource {
  target = module.compute.azurerm_public_ip.this
  values = {
    id         = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/influaudit-prod/providers/Microsoft.Network/publicIPAddresses/influaudit-prod-ip"
    ip_address = "20.40.60.80"
  }
}

override_resource {
  target = module.compute.azurerm_network_interface.this
  values = {
    id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/influaudit-prod/providers/Microsoft.Network/networkInterfaces/influaudit-prod-nic"
  }
}

override_resource {
  target = module.database.azurerm_postgresql_flexible_server.this
  values = {
    id   = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/influaudit-prod/providers/Microsoft.DBforPostgreSQL/flexibleServers/influaudit-prod"
    fqdn = "influaudit-prod.postgres.database.azure.com"
  }
}

override_resource {
  target = module.network.azurerm_network_security_group.this
  values = {
    id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/influaudit-prod/providers/Microsoft.Network/networkSecurityGroups/influaudit-prod"
  }
}

override_resource {
  target = module.network.azurerm_virtual_network.this
  values = {
    id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/influaudit-prod/providers/Microsoft.Network/virtualNetworks/influaudit-prod"
  }
}

override_resource {
  target = azurerm_resource_group.this
  values = {
    id   = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/influaudit-prod"
    name = "influaudit-prod"
  }
}

variables {
  azure_subscription_id = "00000000-0000-0000-0000-000000000000"
  ssh_public_key        = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIN7Vv1p0dCEwl0ZlP2xVKcbDpCe6vB3zVXBQmVeF6mZ7 ci@influaudit"
  cloudflare_api_token  = "0123456789abcdef0123456789abcdef01234567"
  cloudflare_account_id = "0123456789abcdef0123456789abcdef"
  cloudflare_zone_id    = "0123456789abcdef0123456789abcdef"
}

run "stack_applies" {
  command = apply

  # The DSN handed to the application. sslmode=require is not decoration: config.Validate
  # REFUSES to boot prod with sslmode=disable, so a module emitting a bare DSN would build
  # infrastructure the application then declines to run on.
  assert {
    condition     = can(regex("sslmode=require", module.database.dsn))
    error_message = "database.dsn must carry sslmode=require -- the app refuses to boot in prod without it"
  }

  # Every managed Redis is TLS-only. Until platform/redis grew a TLSConfig the Go client
  # could not reach one at all; the module must not claim a plaintext cache is fine.
  assert {
    condition     = module.cache.tls_enabled
    error_message = "cache must report tls_enabled -- prod config.Validate requires redis.tls"
  }

  assert {
    condition     = module.cache.port > 0
    error_message = "cache must expose a port"
  }

  # THE CUTOVER. One A record is what moves production between clouds.
  assert {
    condition     = module.dns.api_fqdn != "" && module.dns.app_fqdn != ""
    error_message = "dns must publish both the api and app fqdns"
  }

  # Object storage is deliberately NOT on the compute cloud. If this ever fails, storage
  # has become part of the migration -- which is the thing the design exists to prevent.
  assert {
    condition     = can(regex("r2.cloudflarestorage.com", module.storage.endpoint))
    error_message = "object storage must stay off the compute cloud -- see modules/storage"
  }

  assert {
    condition     = module.compute.ssh_user == "deploy"
    error_message = "the deploy user is what the forced-command SSH key is pinned to"
  }
}
