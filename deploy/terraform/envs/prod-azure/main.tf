# Production on Azure.
#
# ─────────────────────────────────────────────────────────────────────────────
# READ THIS ALONGSIDE ../prod-gcp/main.tf. The two files differ by FOUR LINES —
# the `source =` on network, compute, database, and cache. Nothing else changes:
# not the sizes, not the retention, not the DNS, not the storage, not one variable.
#
# That diff IS the architecture. Everything else in this repository exists to keep
# it that small.
# ─────────────────────────────────────────────────────────────────────────────

terraform {
  required_version = ">= 1.10"

  required_providers {
    azurerm    = { source = "hashicorp/azurerm", version = "~> 4.0" }
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 4.0" }
  }
}

provider "azurerm" {
  features {}
  subscription_id = var.azure_subscription_id
}

# Azure alone requires every resource to live in a resource group. The modules read
# it out of `tags`, so the shared contract does not have to grow an Azure-shaped
# variable that the other two clouds would have to ignore.
resource "azurerm_resource_group" "this" {
  name     = "influaudit-prod"
  location = var.region
}

provider "cloudflare" {
  api_token = var.cloudflare_api_token
}

locals {
  name = "influaudit-prod"

  tags = {
    app            = "influaudit"
    environment    = "prod"
    managed_by     = "terraform"
    resource_group = azurerm_resource_group.this.name
  }
}

# ---- Cloud-specific: the ONLY four modules whose source changes on migration ----

module "network" {
  source = "../../modules/network/azure" # ← migration changed this line

  name             = local.name
  region           = var.region
  ssh_source_cidrs = var.ssh_source_cidrs
  tags             = local.tags
}

module "database" {
  source = "../../modules/database/azure" # ← and this one

  name       = local.name
  region     = var.region
  network_id = module.network.id

  # t-shirt sizes, never machine types. These values are IDENTICAL in prod-azure.
  instance_size         = "medium"
  storage_gb            = 100
  backup_retention_days = 14
  high_availability     = true

  tags = local.tags
}

module "cache" {
  source = "../../modules/cache/azure" # ← and this one

  name       = local.name
  region     = var.region
  network_id = module.network.id

  memory_gb         = 1
  high_availability = false

  tags = local.tags
}

module "compute" {
  source = "../../modules/compute/azure" # ← and this one

  name           = local.name
  region         = var.region
  network_id     = module.network.id
  firewall_id    = module.network.firewall_id
  vm_size        = "medium"
  disk_gb        = 50
  ssh_public_key = var.ssh_public_key

  tags = local.tags
}

# ---- Cloud-INDEPENDENT: these do not change, ever, on any migration ----------
#
# DNS and object storage are deliberately not on the compute cloud. See the header
# comments in their modules — that is not an oversight, it is the mechanism.

module "storage" {
  source = "../../modules/storage/cloudflare_r2"

  account_id    = var.cloudflare_account_id
  name          = "influaudit-reports"
  backup_bucket = "influaudit-backups"
}

module "dns" {
  source = "../../modules/dns/cloudflare"

  zone_id    = var.cloudflare_zone_id
  api_domain = "api.influaudit.com"
  app_domain = "app.influaudit.com"

  # The Caddyfile defines a Grafana site. A Caddy site with no DNS record never
  # completes its HTTP-01 challenge and sits without a certificate — quietly, forever.
  grafana_domain = "grafana.influaudit.com"

  # THE CUTOVER. This now resolves to the Azure VM. That single value moving is
  # what redirected production off GCP.
  target_ip = module.compute.public_ip
  ttl       = var.dns_ttl
}
