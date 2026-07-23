variable "region" {
  type    = string
  default = "us-east-1"
}

# floci's AWS endpoint. Terraform runs on the host, so it reaches the published
# container port at localhost:4566.
variable "floci_endpoint" {
  type    = string
  default = "http://localhost:4566"
}

# A throwaway public key generated for the local run (deploy/floci/keys). It never
# leaves this machine.
variable "ssh_public_key" {
  type    = string
  default = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBEQv5r9YCUh1HCDX4/pD473oXHb8xac8OlKIu/mTheu floci-local"
}
