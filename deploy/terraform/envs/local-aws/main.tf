# LOCAL AWS — the prod-aws stack applied against floci (http://localhost:4566)
# instead of a real AWS account. Same four cloud modules as prod-aws; the only
# differences are the floci provider endpoints + dummy creds, and that the
# cloud-INDEPENDENT Cloudflare storage/DNS modules are omitted (floci emulates
# AWS/Azure/GCP, not Cloudflare — those are exercised against real Cloudflare, not
# here). State is local (no R2 backend) so the whole thing runs offline.

terraform {
  required_version = ">= 1.10"

  required_providers {
    aws = { source = "hashicorp/aws", version = "~> 5.0" }
  }
}

provider "aws" {
  region     = var.region
  access_key = "test"
  secret_key = "test"

  # floci is not a real AWS account: skip the checks that assume it is.
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true

  # Every AWS API call goes to floci.
  endpoints {
    ec2         = var.floci_endpoint
    rds         = var.floci_endpoint
    elasticache = var.floci_endpoint
    sts         = var.floci_endpoint
    iam         = var.floci_endpoint
  }
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
  source = "../../modules/network/aws"

  name             = local.name
  region           = var.region
  ssh_source_cidrs = ["0.0.0.0/0"]
  tags             = local.tags
}

module "database" {
  source = "../../modules/database/aws"

  name       = local.name
  region     = var.region
  network_id = module.network.id

  instance_size         = "medium"
  storage_gb            = 100
  backup_retention_days = 14
  high_availability     = true

  tags = local.tags
}

# NOTE: the cache module is intentionally omitted from the LOCAL overlay. floci /
# LocalStack-Community does not emulate managed Redis — CreateCacheSubnetGroup
# returns UnsupportedOperation — so it cannot be applied offline. The prod-aws env
# still provisions it; here the migration test runs over network + database +
# compute, the resources floci supports. `terraform plan` in prod-* still validates
# the cache module.

module "compute" {
  source = "../../modules/compute/aws"

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

output "database_host" {
  value = module.database.host
}
