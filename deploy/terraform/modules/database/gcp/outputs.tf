# THE PORTABILITY CONTRACT (outputs half).
#
# BYTE-IDENTICAL across gcp, azure, and aws. Every cloud's database module emits
# exactly these four values, so the env stack — and the SOPS env file it feeds —
# never learn which cloud they are talking to.

output "dsn" {
  description = "Full connection string. sslmode=require always: config.Validate refuses to boot prod with sslmode=disable, because a managed database is reached across a network."
  value       = "postgres://${var.name}:${random_password.db.result}@${local.host}:5432/${var.name}?sslmode=require"
  sensitive   = true
}

output "host" {
  value = local.host
}

output "port" {
  value = 5432
}

output "password" {
  value     = random_password.db.result
  sensitive = true
}
