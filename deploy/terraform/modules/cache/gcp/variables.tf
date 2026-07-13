# THE PORTABILITY CONTRACT. Byte-identical in cache/gcp, cache/azure, cache/aws.
# Only main.tf differs. See database/variables.tf for the reasoning.

variable "name" {
  description = "Instance name."
  type        = string
}

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
