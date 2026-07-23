variable "gcp_project" {
  type = string
}

variable "region" {
  type    = string
  default = "asia-south1" # Mumbai. India-first audience; Razorpay is the payment provider.
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

# Cloudflare holds DNS, R2 (object storage), and — optionally — the Terraform state.
# None of it moves when the compute cloud does.
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
