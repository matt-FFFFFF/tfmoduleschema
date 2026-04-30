terraform {
  required_version = ">= 1.0.0"
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 3.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.0"
    }
  }
}

variable "name" {
  description = "Resource name."
  type        = string
}

variable "location" {
  description = "Azure location."
  type        = string
  default     = "westeurope"
}

variable "tags" {
  description = "Tags to apply."
  type        = map(string)
  default     = {}
}

variable "secret" {
  description = "Sensitive input."
  type        = string
  sensitive   = true
}

variable "session_token" {
  description = "Short-lived token; never persisted to state."
  type        = string
  ephemeral   = true
  sensitive   = true
}

resource "azurerm_resource_group" "rg" {
  name     = var.name
  location = var.location
  tags     = var.tags
}

data "azurerm_client_config" "current" {}

output "resource_group_id" {
  description = "The ID of the created resource group."
  value       = azurerm_resource_group.rg.id
}

output "tenant_id" {
  description = "Current tenant id."
  value       = data.azurerm_client_config.current.tenant_id
  sensitive   = true
}

module "network" {
  source  = "./modules/network"
  version = ""
  name    = var.name
}
