# THE PORTABILITY CONTRACT. Byte-identical in compute/{gcp,azure,aws}.
#
# The VM is CATTLE. It holds no state: Postgres and Redis are managed, object
# storage is off-cloud, and the images come from GHCR. Everything on it is
# reconstructed by deploy/scripts/bootstrap-vm.sh and deploy/scripts/deploy.sh.
#
# That is the property the whole architecture is built to preserve, and it is what
# makes the RTO for "the VM died" fifteen minutes instead of a bad afternoon.

variable "name" {
  type = string
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
variable "zone" {
  description = "Availability zone. Empty lets the provider choose."
  type        = string
  default     = ""
}

variable "network_id" {
  description = "Network to attach to, from the network module."
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
variable "firewall_id" {
  description = "Firewall/security group, from the network module."
  type        = string
}

# t-shirt sizes, never "e2-medium" or "Standard_B2s" or "t3.medium". See
# database/variables.tf — this is the same crux, for the same reason.
variable "vm_size" {
  description = "One of: small | medium | large."
  type        = string
  default     = "medium"

  validation {
    condition     = contains(["small", "medium", "large"], var.vm_size)
    error_message = "vm_size must be small, medium, or large — never a cloud-specific machine type."
  }
}

variable "disk_gb" {
  description = "Root disk. Holds Docker images and container logs, nothing durable."
  type        = number
  default     = 50
}

variable "ssh_public_key" {
  description = "The CI deploy key's public half. Installed for the deploy user, restricted to a forced command."
  type        = string
}

variable "tags" {
  type    = map(string)
  default = {}
}
