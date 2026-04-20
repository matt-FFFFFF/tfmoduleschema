variable "name" {
  type = string
}

resource "azurerm_virtual_network" "vnet" {
  name                = "${var.name}-vnet"
  address_space       = ["10.0.0.0/16"]
  location            = "westeurope"
  resource_group_name = "placeholder"
}

output "vnet_id" {
  value = azurerm_virtual_network.vnet.id
}
