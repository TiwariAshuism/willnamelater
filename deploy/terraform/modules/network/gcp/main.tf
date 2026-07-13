terraform {
  required_providers {
    google = { source = "hashicorp/google", version = "~> 6.0" }
  }
}

locals {
  network_id  = google_compute_network.this.id
  firewall_id = google_compute_firewall.web.id
}

resource "google_compute_network" "this" {
  name                    = var.name
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "this" {
  name          = var.name
  region        = var.region
  network       = google_compute_network.this.id
  ip_cidr_range = cidrsubnet(var.cidr, 8, 1)
}

# Private Service Access: the range Cloud SQL and Memorystore are allocated out of,
# so the data tier is reachable from the VM and from nowhere else on the internet.
resource "google_compute_global_address" "psa" {
  name          = "${var.name}-psa"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.this.id
}

resource "google_service_networking_connection" "psa" {
  network                 = google_compute_network.this.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.psa.name]
}

resource "google_compute_firewall" "web" {
  name    = "${var.name}-web"
  network = google_compute_network.this.name

  allow {
    protocol = "tcp"
    ports    = ["80", "443"]
  }

  source_ranges = ["0.0.0.0/0"]
  target_tags   = ["influaudit"]
}

resource "google_compute_firewall" "ssh" {
  name    = "${var.name}-ssh"
  network = google_compute_network.this.name

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  source_ranges = var.ssh_source_cidrs
  target_tags   = ["influaudit"]
}
