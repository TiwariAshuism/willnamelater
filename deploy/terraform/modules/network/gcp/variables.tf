# THE PORTABILITY CONTRACT. Byte-identical in network/{gcp,azure,aws}.
#
# The network module provides exactly two things: a private path from the VM to the
# managed data tier, and a public path to the VM on 80/443/22 and nothing else.
# Everything else a cloud's networking can express is deliberately not modelled,
# because anything modelled here has to be modelled three times.

variable "name" {
  type = string
}

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

variable "tags" {
  type    = map(string)
  default = {}
}
