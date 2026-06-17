# ---------------------------------------------------------------------------
# AKS (Azure Kubernetes Service) — Multi-cloud support (Phase 4)
# ---------------------------------------------------------------------------
# This file provisions a production-grade AKS cluster equivalent to the EKS
# cluster defined in eks.tf. Enable by setting var.aks_resource_group and
# supplying the required AKS variables.
#
# Prerequisites:
#   terraform init               # installs azurerm provider
#   az login                     # or set ARM_* env vars
# ---------------------------------------------------------------------------

variable "aks_resource_group" {
  description = "Azure Resource Group name for AKS. Leave empty to skip AKS provisioning."
  type        = string
  default     = ""
}

variable "aks_location" {
  description = "Azure region for the AKS cluster (e.g. eastus)."
  type        = string
  default     = "eastus"
}

variable "aks_cluster_name" {
  description = "Name for the AKS cluster."
  type        = string
  default     = "autosre-aks"
}

variable "aks_kubernetes_version" {
  description = "Kubernetes version for the AKS cluster (e.g. '1.29')."
  type        = string
  default     = "1.29"
}

variable "aks_node_vm_size" {
  description = "Azure VM size for AKS worker nodes."
  type        = string
  default     = "Standard_D2s_v3"
}

variable "aks_node_count" {
  description = "Initial node count for the default AKS node pool."
  type        = number
  default     = 2
}

variable "aks_node_min" {
  description = "Minimum node count for AKS autoscaling."
  type        = number
  default     = 1
}

variable "aks_node_max" {
  description = "Maximum node count for AKS autoscaling."
  type        = number
  default     = 5
}

# ---------------------------------------------------------------------------
# Resource group (only when aks_resource_group is set)
# ---------------------------------------------------------------------------

resource "azurerm_resource_group" "autosre" {
  count    = var.aks_resource_group != "" ? 1 : 0
  provider = azurerm

  name     = var.aks_resource_group
  location = var.aks_location

  tags = var.tags
}

# ---------------------------------------------------------------------------
# AKS cluster
# ---------------------------------------------------------------------------

resource "azurerm_kubernetes_cluster" "autosre" {
  count    = var.aks_resource_group != "" ? 1 : 0
  provider = azurerm

  name                = var.aks_cluster_name
  location            = azurerm_resource_group.autosre[0].location
  resource_group_name = azurerm_resource_group.autosre[0].name
  kubernetes_version  = var.aks_kubernetes_version
  dns_prefix          = var.aks_cluster_name

  # System node pool — runs kube-system workloads.
  default_node_pool {
    name                = "default"
    vm_size             = var.aks_node_vm_size
    node_count          = var.aks_node_count
    min_count           = var.aks_node_min
    max_count           = var.aks_node_max
    enable_auto_scaling = true
    os_disk_size_gb     = 50
    os_disk_type        = "Managed"

    # Only Ephemeral OS disk — faster node provisioning, lower cost.
    # Requires node_vm_size that supports ephemeral disks.
    # os_disk_type = "Ephemeral"

    node_labels = {
      role = "default"
    }
  }

  # Workload Identity (equivalent to EKS IRSA — pods get Azure AD identities).
  workload_identity_enabled = true
  oidc_issuer_enabled       = true

  # System-assigned managed identity for AKS control plane.
  identity {
    type = "SystemAssigned"
  }

  # Azure CNI Overlay (equivalent to EKS VPC CNI — pods get VNet IPs).
  network_profile {
    network_plugin      = "azure"
    network_plugin_mode = "overlay"
    load_balancer_sku   = "standard"

    # API server authorized IP ranges: restrict to VPN CIDR (equivalent to EKS master_authorized_networks).
    # Note: api_server_authorized_ip_ranges is set at cluster level, not network_profile.
  }

  # API server access: restrict to VPN CIDR.
  api_server_access_profile {
    authorized_ip_ranges = [var.vpn_cidr]
  }

  # Azure Monitor integration (equivalent to EKS CloudWatch logs).
  azure_active_directory_role_based_access_control {
    managed            = true
    azure_rbac_enabled = true
  }

  # Defender (Microsoft Defender for Containers — equivalent to EKS GuardDuty).
  microsoft_defender {
    log_analytics_workspace_id = azurerm_log_analytics_workspace.autosre[0].id
  }

  oms_agent {
    log_analytics_workspace_id = azurerm_log_analytics_workspace.autosre[0].id
  }

  tags = var.tags
}

# Log Analytics workspace for AKS monitoring.
resource "azurerm_log_analytics_workspace" "autosre" {
  count    = var.aks_resource_group != "" ? 1 : 0
  provider = azurerm

  name                = "${var.aks_cluster_name}-logs"
  location            = azurerm_resource_group.autosre[0].location
  resource_group_name = azurerm_resource_group.autosre[0].name
  sku                 = "PerGB2018"
  retention_in_days   = 30

  tags = var.tags
}

# ---------------------------------------------------------------------------
# AKS outputs
# ---------------------------------------------------------------------------

output "aks_cluster_endpoint" {
  description = "AKS cluster API server FQDN."
  value       = var.aks_resource_group != "" ? azurerm_kubernetes_cluster.autosre[0].fqdn : ""
  sensitive   = true
}

output "aks_kube_config" {
  description = "AKS kubeconfig (raw). Use for local kubectl access."
  value       = var.aks_resource_group != "" ? azurerm_kubernetes_cluster.autosre[0].kube_config_raw : ""
  sensitive   = true
}
