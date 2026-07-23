variable "region" {
  type    = string
  default = "eastus"
}

# floci-az serves ARM metadata (over HTTPS) here; the azurerm provider discovers
# the rest of the endpoints from it.
variable "floci_metadata_host" {
  type    = string
  default = "localhost:4577"
}

variable "ssh_public_key" {
  type    = string
  default = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBEQv5r9YCUh1HCDX4/pD473oXHb8xac8OlKIu/mTheu floci-local"
}
