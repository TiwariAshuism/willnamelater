terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.0" }
  }
}

locals {
  sizes = {
    small  = "Standard_B1ms"
    medium = "Standard_B2s"
    large  = "Standard_D4s_v3"
  }

  public_ip = azurerm_public_ip.this.ip_address
}

resource "azurerm_public_ip" "this" {
  name                = "${var.name}-ip"
  location            = var.region
  resource_group_name = var.tags["resource_group"]
  allocation_method   = "Static"
  sku                 = "Standard"
  tags                = var.tags
}

resource "azurerm_network_interface" "this" {
  name                = "${var.name}-nic"
  location            = var.region
  resource_group_name = var.tags["resource_group"]
  tags                = var.tags

  ip_configuration {
    name = "primary"
    # The network module's `id` output is the DELEGATED data subnet (Flexible Server
    # is VNet-injected). The VM belongs on the app subnet, which is derived from it.
    subnet_id                     = replace(var.network_id, "-data", "-app")
    private_ip_address_allocation = "Dynamic"
    public_ip_address_id          = azurerm_public_ip.this.id
  }
}

resource "azurerm_linux_virtual_machine" "this" {
  name                = var.name
  location            = var.region
  resource_group_name = var.tags["resource_group"]
  size                = local.sizes[var.vm_size]
  admin_username      = "deploy"
  tags                = var.tags

  network_interface_ids = [azurerm_network_interface.this.id]

  admin_ssh_key {
    username   = "deploy"
    public_key = var.ssh_public_key
  }

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "Premium_LRS"
    disk_size_gb         = var.disk_gb
  }

  source_image_reference {
    publisher = "Canonical"
    offer     = "ubuntu-24_04-lts"
    sku       = "server"
    version   = "latest"
  }
}
