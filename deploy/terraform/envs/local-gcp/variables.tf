variable "region" {
  type    = string
  default = "us-central1"
}

variable "gcp_project" {
  type    = string
  default = "floci-local"
}

variable "floci_endpoint" {
  type    = string
  default = "http://localhost:4588"
}

variable "ssh_public_key" {
  type    = string
  default = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBEQv5r9YCUh1HCDX4/pD473oXHb8xac8OlKIu/mTheu floci-local"
}
