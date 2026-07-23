# LOCAL GCP — the prod-gcp stack applied against floci-gcp (localhost:4588).
# Same modules as prod-gcp minus the Cloudflare storage/DNS (not floci's domain)
# and the cache module (managed Redis is not emulated). Local state, offline.

terraform {
  required_version = ">= 1.10"

  required_providers {
    google = { source = "hashicorp/google", version = "~> 6.0" }
  }
}

provider "google" {
  project      = var.gcp_project
  region       = var.region
  access_token = "floci-dummy-token" # floci does not validate credentials.

  # Every Google API call goes to floci-gcp (one port, path-routed).
  compute_custom_endpoint            = "${var.floci_endpoint}/compute/v1/"
  service_networking_custom_endpoint = "${var.floci_endpoint}/servicenetworking/v1/"
  sql_custom_endpoint                = "${var.floci_endpoint}/sql/v1beta4/"
}

locals {
  name = "influaudit-local"

  tags = {
    app         = "influaudit"
    environment = "local"
    managed_by  = "terraform"
  }
}

module "network" {
  source = "../../modules/network/gcp"

  name             = local.name
  region           = var.region
  ssh_source_cidrs = ["0.0.0.0/0"]
  tags             = local.tags
}

module "database" {
  source = "../../modules/database/gcp"

  name       = local.name
  region     = var.region
  network_id = module.network.id

  instance_size         = "medium"
  storage_gb            = 100
  backup_retention_days = 14
  high_availability     = true

  tags = local.tags
}

module "compute" {
  source = "../../modules/compute/gcp"

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
