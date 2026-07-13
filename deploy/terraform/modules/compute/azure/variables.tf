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

variable "region" {
  type = string
}

variable "zone" {
  description = "Availability zone. Empty lets the provider choose."
  type        = string
  default     = ""
}

variable "network_id" {
  description = "Network to attach to, from the network module."
  type        = string
}

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
