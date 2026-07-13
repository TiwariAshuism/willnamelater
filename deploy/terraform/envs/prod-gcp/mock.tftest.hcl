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

mock_provider "google" {}
mock_provider "cloudflare" {}
mock_provider "random" {}

# The google provider validates the FORMAT of a network id against a regex. The mock
# generator produces a random string, which is not a project path.
override_resource {
  target = module.network.google_compute_network.this
  values = {
    id   = "projects/influaudit-test/global/networks/influaudit-prod"
    name = "influaudit-prod"
  }
}

# Memorystore's port is a computed attribute, and the mock generator returns 0 for an
# unknown number. Supply the real one.
override_resource {
  target = module.cache.google_redis_instance.this
  values = {
    host        = "10.20.1.5"
    port        = 6379
    auth_string = "mock-auth-string"
  }
}

variables {
  gcp_project           = "influaudit-test"
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
