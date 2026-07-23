# THE PORTABILITY CONTRACT. Byte-identical in network/{gcp,azure,aws}.
#
# The network module provides exactly two things: a private path from the VM to the
# managed data tier, and a public path to the VM on 80/443/22 and nothing else.
# Everything else a cloud's networking can express is deliberately not modelled,
# because anything modelled here has to be modelled three times.

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

variable "cidr" {
  description = "Private address space."
  type        = string
  default     = "10.20.0.0/16"
}

variable "ssh_source_cidrs" {
  description = <<-EOT
    Who may reach port 22. The default is the whole internet because CI deploys from
    GitHub's runners, whose addresses are not stable. The key on the far end is
    restricted to a forced command (deploy/scripts/ssh-entrypoint.sh), so an open
    port 22 is not an open shell — but narrow this anyway if you have a bastion.
  EOT
  type        = list(string)
  default     = ["0.0.0.0/0"]
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
variable "tags" {
  type    = map(string)
  default = {}
}
