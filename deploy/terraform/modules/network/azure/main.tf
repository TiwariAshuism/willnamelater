terraform {
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
