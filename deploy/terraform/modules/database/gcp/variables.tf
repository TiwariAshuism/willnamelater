# THE PORTABILITY CONTRACT.
#
# This file is BYTE-IDENTICAL in database/gcp, database/azure, and database/aws.
# So is outputs.tf. Only main.tf differs.
#
# That is what makes a cloud migration mechanical: the env stack sets these
# variables once and changes ONE line — the `source =` — to move clouds. If you
# ever find yourself wanting to add a variable that only one cloud understands,
# that is the moment the contract breaks, and the answer is almost always to
# express the intent generically and let that cloud's main.tf translate it.
#
# .github/workflows/terraform.yml asserts this file is identical across the three,
# so the contract cannot rot silently.

variable "name" {
  description = "Instance name. Also the database name."
  type        = string
}

# Unused on at least one cloud, and that is the contract working rather than a mistake:
# this file is BYTE-IDENTICAL across gcp/azure/aws, so every cloud must ACCEPT every
# input even where its provider expresses the same intent differently — AWS takes the
# region from the provider block, GCP scopes firewall rules by target_tag rather than
# by id.
#
# The ignore is scoped to this one variable rather than switched off for the module,
# because an unused variable in a portability contract is usually a cloud quietly NOT
# doing something the other two do. That is exactly how the Azure cache was caught
# sitting on the public internet with no private endpoint.
# tflint-ignore: terraform_unused_declarations
variable "region" {
  description = "Cloud region. The one value that is inherently provider-specific."
  type        = string
}

variable "network_id" {
  description = "Private network to attach to, from the network module."
  type        = string
}

variable "postgres_version" {
  description = "Major version. 16 everywhere."
  type        = string
  default     = "16"
}

# THE CRUX OF THE WHOLE DESIGN.
#
# t-shirt sizes, never "db-f1-micro" or "Standard_D2ds_v4" or "db.t4g.medium".
# Each cloud's main.tf owns a locals.sizes lookup that translates. The env stack
# therefore never names a machine type, and moving clouds does not require
# re-deciding what "medium" means — it requires changing a source path.
variable "instance_size" {
  description = "One of: small | medium | large."
  type        = string

  validation {
    condition     = contains(["small", "medium", "large"], var.instance_size)
    error_message = "instance_size must be small, medium, or large — never a cloud-specific machine type."
  }
}

variable "storage_gb" {
  description = "Allocated storage in GB."
  type        = number
  default     = 50
}

variable "backup_retention_days" {
  description = <<-EOT
    Retention for the CLOUD's automated backups.

    Note what this does NOT buy you: a Cloud SQL backup cannot be restored into
    Azure. Cross-cloud recovery rests entirely on the portable pg_dump written to
    object storage by deploy/scripts/backup.sh. This is the layer that makes you
    feel safe; that one is the layer that lets you leave.
  EOT
  type        = number
  default     = 14
}

variable "high_availability" {
  description = "Provision a standby in a second zone with automatic failover."
  type        = bool
  default     = false
}

variable "tags" {
  description = "Tags/labels applied to every resource."
  type        = map(string)
  default     = {}
}
