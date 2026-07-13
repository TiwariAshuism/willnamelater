# Remote state — on Cloudflare R2, and deliberately NOT on GCS.
#
# THE RECURSION PROBLEM: if the state that describes your GCP infrastructure lives
# in a GCS bucket, then migrating off GCP has a bootstrap dependency on GCP. You
# cannot `terraform destroy` the old cloud without the state, and the state is in the
# cloud you are destroying. That is a bad afternoon waiting to happen.
#
# R2 has no such circularity: it is on none of the three clouds, it is the same
# account that already holds the object store and the backups, and it speaks the S3
# protocol so Terraform's `s3` backend drives it with the skip_* flags below.
#
# `use_lockfile = true` (Terraform >= 1.10) gives S3-native state locking, so there
# is no DynamoDB table to provision — which is fortunate, because a DynamoDB lock
# table would re-introduce exactly the AWS dependency this avoids.
#
# Terraform Cloud's free tier is an equally good answer. R2 is chosen here only
# because it is one fewer account, and it is consistent with everything else.
#
# prod-gcp/backend.tf is IDENTICAL to this file except for the `key`.

terraform {
  backend "s3" {
    bucket = "influaudit-tfstate"
    key    = "prod-azure/terraform.tfstate"

    endpoints = {
      s3 = "https://REPLACE_ACCOUNT_ID.r2.cloudflarestorage.com"
    }

    region       = "auto"
    use_lockfile = true

    # R2 is not AWS, and these validations assume it is.
    skip_credentials_validation = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    skip_s3_checksum            = true
    skip_metadata_api_check     = true
  }
}
