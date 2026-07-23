# THE PORTABILITY CONTRACT (outputs half). Byte-identical across the three clouds.
#
# tls_enabled is always true and is emitted anyway, as documentation: EVERY managed
# Redis is TLS-only (Azure disables the plaintext port outright). The Go client
# could not speak TLS at all until internal/platform/redis grew a TLSConfig, which
# is the single change that made a managed cache reachable from this application.

output "host" {
  value = local.host
}

output "port" {
  value = local.port
}

output "password" {
  value     = local.password
  sensitive = true
}

output "tls_enabled" {
  value = true
}
