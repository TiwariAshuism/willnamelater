terraform {
  required_providers {
    google = { source = "hashicorp/google", version = "~> 6.0" }
  }
}

locals {
  sizes = {
    small  = "e2-small"
    medium = "e2-medium"
    large  = "e2-standard-4"
  }

  public_ip = google_compute_instance.this.network_interface[0].access_config[0].nat_ip
}

resource "google_compute_instance" "this" {
  name         = var.name
  machine_type = local.sizes[var.vm_size]
  zone         = var.zone != "" ? var.zone : "${var.region}-a"

  # The firewall rules in the network module target this tag.
  tags = ["influaudit"]

  boot_disk {
    initialize_params {
      image = "ubuntu-os-cloud/ubuntu-2404-lts-amd64"
      size  = var.disk_gb
      type  = "pd-balanced"
    }
  }

  network_interface {
    network = var.network_id

    # A public IP, because Caddy terminates TLS on this box. There is no load
    # balancer: at one VM it would be a second thing to pay for, configure, and
    # migrate, and it would terminate the TLS that Caddy already terminates for
    # free. Add one at the point you have two VMs — see ARCHITECTURE.md.
    access_config {}
  }

  metadata = {
    ssh-keys = "deploy:${var.ssh_public_key}"
  }

  labels = var.tags

  lifecycle {
    # The disk holds Docker images and logs. Replacing the VM is a normal, cheap
    # operation — never something to protect against.
    ignore_changes = [boot_disk[0].initialize_params[0].image]
  }
}
