# THE PORTABILITY CONTRACT. Byte-identical in cache/gcp, cache/azure, cache/aws.
# Only main.tf differs. See database/variables.tf for the reasoning.

variable "name" {
  description = "Instance name."
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
  type = string
}

variable "network_id" {
  description = "Private network to attach to, from the network module."
  type        = string
}

variable "memory_gb" {
  description = "Cache size in GB."
  type        = number
  default     = 1
}

variable "high_availability" {
  description = "Provision a replica with automatic failover."
  type        = bool
  default     = false
}

variable "tags" {
  type    = map(string)
  default = {}
}
