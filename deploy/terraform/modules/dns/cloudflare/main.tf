# DNS. ONE implementation, on purpose.
#
# There is no dns/gcp, dns/azure, or dns/aws, and there never should be. DNS is the
# instrument of the migration — the cutover IS a DNS change — so it must not live on
# the cloud being migrated away from. Putting the zone in Cloud DNS and then trying
# to leave GCP means changing the nameservers of the domain you are actively serving
# from, during the window in which you are least able to absorb a mistake.
#
# Cloudflare instead: free, fast, cloud-independent, and the same account already
# holds R2 (object storage) and can hold the Terraform state. On migration day this
# module's only change is the value of one A record.
#
# The same reasoning applies to the container registry (GHCR), the object store
# (R2), the Terraform state, and the observability stack. The pattern: EVERY
# cloud-specific thing is either behind a module with an identical interface, or is
# not on the cloud at all. This module is the second kind.

terraform {
  # Pinned so a module cannot be planned by a Terraform old enough to
  # mis-handle it. Same value in every module, on every cloud.
  required_version = ">= 1.10"

  required_providers {
    cloudflare = { source = "cloudflare/cloudflare", version = "~> 4.0" }
  }
}

variable "zone_id" {
  description = "Cloudflare zone for the apex domain."
  type        = string
}

variable "api_domain" {
  description = "FQDN the API is served on, e.g. api.influaudit.com."
  type        = string
}

variable "app_domain" {
  description = "FQDN the web app is served on, e.g. app.influaudit.com."
  type        = string
}

variable "grafana_domain" {
  description = <<-EOT
    FQDN Grafana is served on. The Caddyfile defines a site for it, and a Caddy site
    with no DNS record never completes an HTTP-01 challenge — so it would sit there
    without a certificate, quietly, forever. Empty disables the record and you should
    then also remove the site from the Caddyfile.
  EOT
  type        = string
  default     = ""
}

variable "target_ip" {
  description = "The VM's public IP, from the compute module. THIS IS THE CUTOVER: changing it moves production to another cloud."
  type        = string
}

variable "ttl" {
  description = <<-EOT
    Seconds. Drop this to 60 the day BEFORE a migration, so the cutover propagates in
    a minute rather than in whatever the old TTL was. Raise it again afterwards.
  EOT
  type        = number
  default     = 300
}

resource "cloudflare_record" "api" {
  zone_id = var.zone_id
  name    = var.api_domain
  type    = "A"
  content = var.target_ip
  ttl     = var.ttl

  # NOT proxied. Caddy on the VM obtains its own Let's Encrypt certificate over
  # HTTP-01, which requires that the origin be directly reachable on port 80.
  # Turning the orange cloud on breaks issuance and renewal, silently, ~60 days
  # later. If you want Cloudflare in front, switch Caddy to a DNS-01 challenge
  # first.
  proxied = false
}

resource "cloudflare_record" "app" {
  zone_id = var.zone_id
  name    = var.app_domain
  type    = "A"
  content = var.target_ip
  ttl     = var.ttl
  proxied = false
}

resource "cloudflare_record" "grafana" {
  count = var.grafana_domain != "" ? 1 : 0

  zone_id = var.zone_id
  name    = var.grafana_domain
  type    = "A"
  content = var.target_ip
  ttl     = var.ttl
  proxied = false
}

output "api_fqdn" {
  value = cloudflare_record.api.hostname
}

output "app_fqdn" {
  value = cloudflare_record.app.hostname
}
