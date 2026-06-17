# ---------------------------------------------------------------------------
# GKE (Google Kubernetes Engine) — Multi-cloud support (Phase 4)
# ---------------------------------------------------------------------------
# This file provisions a production-grade GKE cluster equivalent to the EKS
# cluster defined in eks.tf. Enable by setting var.cloud_provider = "gke"
# and supplying the required GKE variables.
#
# Prerequisites:
#   terraform init                          # installs google provider
#   gcloud auth application-default login   # or set GOOGLE_CREDENTIALS env var
# ---------------------------------------------------------------------------

variable "gke_project" {
  description = "GCP project ID for the GKE cluster."
  type        = string
  default     = ""
}

variable "gke_region" {
  description = "GCP region for the GKE cluster (e.g. us-central1)."
  type        = string
  default     = "us-central1"
}

variable "gke_cluster_name" {
  description = "Name for the GKE cluster."
  type        = string
  default     = "autosre-gke"
}

variable "gke_kubernetes_version" {
  description = "Minimum master version for GKE (e.g. '1.29')."
  type        = string
  default     = "1.29"
}

variable "gke_node_machine_type" {
  description = "GCE machine type for GKE nodes."
  type        = string
  default     = "e2-standard-2"
}

variable "gke_node_count" {
  description = "Initial node count per zone in the default node pool."
  type        = number
  default     = 2
}

variable "gke_node_min" {
  description = "Minimum node count for GKE autoscaling."
  type        = number
  default     = 1
}

variable "gke_node_max" {
  description = "Maximum node count for GKE autoscaling."
  type        = number
  default     = 5
}

# ---------------------------------------------------------------------------
# Only provision GKE resources when gke_project is set.
# This avoids errors when deploying to AWS only.
# ---------------------------------------------------------------------------

resource "google_container_cluster" "autosre" {
  count    = var.gke_project != "" ? 1 : 0
  provider = google

  name     = var.gke_cluster_name
  project  = var.gke_project
  location = var.gke_region

  # Disable the default node pool — use a separately managed pool below
  # so we can configure autoscaling and image type independently.
  remove_default_node_pool = true
  initial_node_count       = 1

  min_master_version = var.gke_kubernetes_version

  # Workload Identity replaces node-level service accounts for pod-level IAM.
  workload_identity_config {
    workload_pool = "${var.gke_project}.svc.id.goog"
  }

  # Enable Shielded Nodes for node integrity verification.
  enable_shielded_nodes = true

  # Network policy: enforce Calico NetworkPolicies (equivalent to EKS VPC CNI).
  network_policy {
    enabled  = true
    provider = "CALICO"
  }

  # Binary Authorization prevents unsigned images from running.
  binary_authorization {
    evaluation_mode = "PROJECT_SINGLETON_POLICY_ENFORCE"
  }

  # Master authorized networks: restrict API server access to VPN CIDR.
  master_authorized_networks_config {
    cidr_blocks {
      display_name = "vpn"
      cidr_block   = var.vpn_cidr
    }
  }

  # Private cluster: nodes have no public IPs; only master endpoint is public.
  private_cluster_config {
    enable_private_nodes    = true
    enable_private_endpoint = false
    master_ipv4_cidr_block  = "172.16.0.0/28"
  }

  # Enable audit logging to Cloud Logging (equivalent to EKS CloudWatch audit logs).
  logging_config {
    enable_components = ["SYSTEM_COMPONENTS", "WORKLOADS"]
  }

  monitoring_config {
    enable_components = ["SYSTEM_COMPONENTS"]
  }

  resource_labels = merge(var.tags, {
    cluster = var.gke_cluster_name
  })
}

resource "google_container_node_pool" "autosre_default" {
  count    = var.gke_project != "" ? 1 : 0
  provider = google

  name       = "${var.gke_cluster_name}-default"
  project    = var.gke_project
  location   = var.gke_region
  cluster    = google_container_cluster.autosre[0].name

  initial_node_count = var.gke_node_count

  autoscaling {
    min_node_count = var.gke_node_min
    max_node_count = var.gke_node_max
  }

  management {
    auto_repair  = true
    auto_upgrade = true
  }

  node_config {
    machine_type = var.gke_node_machine_type
    disk_size_gb = 50
    disk_type    = "pd-ssd"

    # Use Workload Identity on each node.
    workload_metadata_config {
      mode = "GKE_METADATA"
    }

    # Shielded instance config (integrity monitoring + secure boot).
    shielded_instance_config {
      enable_secure_boot          = true
      enable_integrity_monitoring = true
    }

    oauth_scopes = [
      "https://www.googleapis.com/auth/cloud-platform",
    ]

    labels = merge(var.tags, { role = "default" })

    metadata = {
      disable-legacy-endpoints = "true"
    }
  }

  upgrade_settings {
    max_surge       = 1
    max_unavailable = 0
  }
}

# ---------------------------------------------------------------------------
# GKE outputs (used by Helm provider when deploying to GKE)
# ---------------------------------------------------------------------------

output "gke_cluster_endpoint" {
  description = "GKE cluster API server endpoint."
  value       = var.gke_project != "" ? google_container_cluster.autosre[0].endpoint : ""
  sensitive   = true
}

output "gke_cluster_ca_certificate" {
  description = "GKE cluster CA certificate (base64-encoded)."
  value       = var.gke_project != "" ? google_container_cluster.autosre[0].master_auth[0].cluster_ca_certificate : ""
  sensitive   = true
}
