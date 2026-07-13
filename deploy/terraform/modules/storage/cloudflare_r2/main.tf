# Object storage. ONE implementation, on purpose.
#
# THE SINGLE MOST IMPORTANT DECISION IN THIS DIRECTORY.
#
# The Go client (internal/platform/storage) speaks the S3 API over hand-rolled
# SigV4. AWS S3, Cloudflare R2, MinIO, and GCS-in-interop-mode all answer it
# unchanged. Azure Blob is the ONE major store with no S3 face — so a deployment
# that used the compute cloud's native store would need a whole new adapter (a
# second signing scheme, a SAS model, a provider switch, new known-answer tests) the
# day it moved to Azure.
#
# The alternative — and the choice made here — is to put the bucket somewhere that
# is not any of the three clouds. Then:
#
#   * moving compute GCP -> Azure -> AWS is NOT a storage migration at all. No data
#     to copy, no share URLs to re-mint, no adapter to write. Storage simply does
#     not participate.
#   * egress is free (R2 charges none), and report PDFs served to browsers are
#     exactly the workload where egress is the whole bill.
#   * the nightly pg_dump backups land HERE too, which means on migration day the
#     backups are already somewhere the new cloud can reach. That is what makes the
#     DR story and the migration story the same story.
#
# If a data-residency rule ever forbids R2, the fallback is NOT an Azure Blob
# adapter — it is the cloud's own S3-compatible endpoint: S3 natively, GCS in
# interop mode with HMAC keys (which is precisely why that mode exists), or MinIO on
# a disk. The existing client handles all three with no code change. Azure Blob's
# lack of an S3 API is Azure's problem, and it should stay Azure's problem.

terraform {
  required_providers {
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 4.0" }
  }
}

variable "account_id" {
  type = string
}

variable "name" {
  description = "Bucket for published report PDFs."
  type        = string
}

variable "backup_bucket" {
  description = "Bucket for the portable pg_dump backups. Separate from reports so a lifecycle rule on one cannot expire the other."
  type        = string
}

variable "location" {
  description = "R2 location hint. APAC given the India-first audience."
  type        = string
  default     = "APAC"
}

resource "cloudflare_r2_bucket" "reports" {
  account_id = var.account_id
  name       = var.name
  location   = var.location
}

resource "cloudflare_r2_bucket" "backups" {
  account_id = var.account_id
  name       = var.backup_bucket
  location   = var.location
}

output "endpoint" {
  description = "S3-compatible endpoint. Goes straight into INFLUAUDIT_STORAGE__ENDPOINT."
  value       = "https://${var.account_id}.r2.cloudflarestorage.com"
}

output "region" {
  description = "R2 signs with the literal region \"auto\"."
  value       = "auto"
}

output "bucket" {
  value = cloudflare_r2_bucket.reports.name
}

output "backup_bucket" {
  value = cloudflare_r2_bucket.backups.name
}
