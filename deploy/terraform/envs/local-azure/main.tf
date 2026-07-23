# LOCAL AZURE — the prod-azure stack applied against floci-az (localhost:4577).
# The azurerm provider discovers the emulator's ARM endpoints over HTTPS via
# metadata_host; the self-signed CA is handed to Terraform with ARM_CA_BUNDLE
# (see deploy/floci/README.md). Cloudflare + cache modules omitted; local state.

terraform {
  required_version = ">= 1.10"

  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.0" }
  }
}

provider "azurerm" {
  features {}

  # Dummy credentials — floci does not validate them.
  subscription_id = "00000000-0000-0000-0000-000000000000"
  tenant_id       = "00000000-0000-0000-0000-000000000000"
  client_id       = "00000000-0000-0000-0000-000000000000"
  client_secret   = "dummy"
  use_cli         = false
  use_msi         = false

  # v4 replacement for skip_provider_registration.
  resource_provider_registrations = "none"

  # Discover the emulator's ARM endpoints (over HTTPS) instead of real Azure.
  metadata_host = var.floci_metadata_host
}

resource "azurerm_resource_group" "this" {
  name     = "influaudit-local"
  location = var.region
}

locals {
  name = "influaudit-local"

  tags = {
    app            = "influaudit"
    environment    = "local"
    managed_by     = "terraform"
    resource_group = azurerm_resource_group.this.name
  }
}

module "network" {
  source = "../../modules/network/azure"

  name             = local.name
  region           = var.region
  ssh_source_cidrs = ["0.0.0.0/0"]
  tags             = local.tags
}

# NOTE: the database module is omitted from the LOCAL Azure overlay. floci-az DOES
# create the PostgreSQL Flexible Server (the emulator returns provisioningState
# "Succeeded"), but it answers the create with HTTP 201 where the azurerm provider
# expects the async 200/202 shape, so Terraform reports it as an error even though
# the resource exists. A floci status-code quirk, not an IaC problem — excluded
# here so the local apply is clean. prod-azure still provisions it; `plan` validates
# the module.

module "compute" {
  source = "../../modules/compute/azure"

  name           = local.name
  region         = var.region
  network_id     = module.network.id
  firewall_id    = module.network.firewall_id
  vm_size        = "medium"
  disk_gb        = 50
  ssh_public_key = var.ssh_public_key

  tags = local.tags
}

output "vm_public_ip" {
  value = module.compute.public_ip
}
