# Production on GCP.
#
# ─────────────────────────────────────────────────────────────────────────────
# READ THIS ALONGSIDE ../prod-azure/main.tf. The two files differ by FOUR LINES —
# the `source =` on network, compute, database, and cache. Nothing else changes:
# not the sizes, not the retention, not the DNS, not the storage, not one variable.
#
# That diff IS the architecture. Everything else in this repository exists to keep
# it that small.
# ─────────────────────────────────────────────────────────────────────────────

terraform {
  required_version = ">= 1.10"

  required_providers {
    google     = { source = "hashicorp/google", version = "~> 6.0" }
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 4.0" }
  }
}

provider "google" {
  project = var.gcp_project
  region  = var.region
}

provider "cloudflare" {
  api_token = var.cloudflare_api_token
}

locals {
  name = "influaudit-prod"

  tags = {
    app         = "influaudit"
    environment = "prod"
    managed_by  = "terraform"
  }
}

# ---- Cloud-specific: the ONLY four modules whose source changes on migration ----

module "network" {
  source = "../../modules/network/gcp" # ← migration changes this line

  name             = local.name
  region           = var.region
  ssh_source_cidrs = var.ssh_source_cidrs
  tags             = local.tags
}

module "database" {
  source = "../../modules/database/gcp" # ← and this one

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
  source = "../../modules/cache/gcp" # ← and this one

  name       = local.name
  region     = var.region
  network_id = module.network.id

  memory_gb         = 1
  high_availability = false

  tags = local.tags
}

module "compute" {
  source = "../../modules/compute/gcp" # ← and this one

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

  # THE CUTOVER. On migration day this resolves to the Azure VM instead, and that
  # single value moving is what redirects production.
  target_ip = module.compute.public_ip
  ttl       = var.dns_ttl
}
