terraform {
  # Pinned so a module cannot be planned by a Terraform old enough to
  # mis-handle it. Same value in every module, on every cloud.
  required_version = ">= 1.10"

  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.0" }
  }
}

locals {
  # Azure Flexible Server is VNet-INJECTED, not peered: the database module attaches
  # to a delegated subnet rather than to the VNet itself. So the contract's single
  # network id is that subnet.
  network_id  = azurerm_subnet.data.id
  firewall_id = azurerm_network_security_group.this.id
}

resource "azurerm_virtual_network" "this" {
  name                = var.name
  location            = var.region
  resource_group_name = var.tags["resource_group"]
  address_space       = [var.cidr]
  tags                = var.tags
}

resource "azurerm_subnet" "app" {
  name                 = "${var.name}-app"
  resource_group_name  = var.tags["resource_group"]
  virtual_network_name = azurerm_virtual_network.this.name
  address_prefixes     = [cidrsubnet(var.cidr, 8, 1)]
}

resource "azurerm_subnet" "data" {
  name                 = "${var.name}-data"
  resource_group_name  = var.tags["resource_group"]
  virtual_network_name = azurerm_virtual_network.this.name
  address_prefixes     = [cidrsubnet(var.cidr, 8, 2)]

  delegation {
    name = "postgres"
    service_delegation {
      name    = "Microsoft.DBforPostgreSQL/flexibleServers"
      actions = ["Microsoft.Network/virtualNetworks/subnets/join/action"]
    }
  }
}

# Deliberate, and annotated so the security scan can be ENFORCED in CI rather than being
# a report nobody reads. An unexplained finding and an accepted risk look identical in a
# scanner's output, and only one of them is acceptable.
#
# trivy:ignore:AVD-AZU-0047 Unrestricted ingress on 80/443. This is a public web server.
# trivy:ignore:AVD-AZU-0050 SSH open by default. CI deploys from GitHub's runners, whose
#   addresses are not stable. The mitigation is that the key on the far end is pinned to
#   a forced command (deploy/scripts/ssh-entrypoint.sh): it can ask for a deploy or a
#   rollback and nothing else, and cannot obtain a shell. That is a mitigation, not a
#   fix — narrow var.ssh_source_cidrs the day you have a bastion or a self-hosted runner.
resource "azurerm_network_security_group" "this" {
  name                = var.name
  location            = var.region
  resource_group_name = var.tags["resource_group"]
  tags                = var.tags

  security_rule {
    name                       = "web"
    priority                   = 100
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_ranges    = ["80", "443"]
    source_address_prefix      = "*"
    destination_address_prefix = "*"
  }

  security_rule {
    name                       = "ssh"
    priority                   = 110
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "22"
    source_address_prefixes    = var.ssh_source_cidrs
    destination_address_prefix = "*"
  }
}

resource "azurerm_subnet_network_security_group_association" "app" {
  subnet_id                 = azurerm_subnet.app.id
  network_security_group_id = azurerm_network_security_group.this.id
}
