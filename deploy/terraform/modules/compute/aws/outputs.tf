# THE PORTABILITY CONTRACT (outputs half). Byte-identical across the three clouds.
#
# public_ip is what the DNS module points at — and, on migration day, the single
# value whose change constitutes the cutover.

output "public_ip" {
  value = local.public_ip
}

output "ssh_user" {
  value = "deploy"
}
