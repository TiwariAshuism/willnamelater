variable "azure_subscription_id" {
  type = string
}

variable "region" {
  type    = string
  default = "centralindia" # Same audience, same reasoning as prod-gcp's asia-south1.
}

variable "ssh_public_key" {
  description = "The CI deploy key's public half."
  type        = string
}

variable "ssh_source_cidrs" {
  type    = list(string)
  default = ["0.0.0.0/0"]
}

variable "dns_ttl" {
  description = "Drop to 60 the day before a migration."
  type        = number
  default     = 300
}

# Identical to prod-gcp: Cloudflare holds DNS and R2, and neither moved when the
# compute cloud did. That is the point.
variable "cloudflare_api_token" {
  type      = string
  sensitive = true
}

variable "cloudflare_account_id" {
  type = string
}

variable "cloudflare_zone_id" {
  type = string
}
