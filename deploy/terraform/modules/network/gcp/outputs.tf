# THE PORTABILITY CONTRACT (outputs half). Byte-identical across the three clouds.
#
# `id` is what the database and cache modules attach to. It is a single string on
# every cloud, even where the underlying concept is a list (AWS subnet groups),
# because a contract that bends to one cloud's shape has stopped being a contract.
# The AWS module comma-joins and the AWS consumers split — the ugliness is confined
# to the one cloud that needs it.

output "id" {
  value = local.network_id
}

output "firewall_id" {
  value = local.firewall_id
}
