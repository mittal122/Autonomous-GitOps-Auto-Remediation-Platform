# ---------------------------------------------------------------------------
# EKS Cluster
# ---------------------------------------------------------------------------
resource "aws_eks_cluster" "this" {
  name     = var.cluster_name
  version  = var.cluster_version
  role_arn = aws_iam_role.eks_control_plane.arn

  vpc_config {
    subnet_ids              = concat(aws_subnet.private[*].id, aws_subnet.public[*].id)
    endpoint_private_access = true
    endpoint_public_access  = true
  }

  enabled_cluster_log_types = ["api", "audit", "authenticator", "controllerManager", "scheduler"]

  depends_on = [
    aws_iam_role_policy_attachment.eks_cluster_policy,
    aws_iam_role_policy_attachment.eks_vpc_resource_controller,
    aws_cloudwatch_log_group.eks,
  ]

  tags = {
    Name = var.cluster_name
  }
}

resource "aws_cloudwatch_log_group" "eks" {
  name              = "/aws/eks/${var.cluster_name}/cluster"
  retention_in_days = 7
}

# ---------------------------------------------------------------------------
# OIDC provider — required for IRSA (IAM Roles for Service Accounts)
# ---------------------------------------------------------------------------
data "tls_certificate" "eks" {
  url = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

resource "aws_iam_openid_connect_provider" "eks" {
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = [data.tls_certificate.eks.certificates[0].sha1_fingerprint]
  url             = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

# ---------------------------------------------------------------------------
# Managed Node Group
# ---------------------------------------------------------------------------
resource "aws_eks_node_group" "default" {
  cluster_name    = aws_eks_cluster.this.name
  node_group_name = "${var.cluster_name}-default"
  node_role_arn   = aws_iam_role.eks_node.arn
  subnet_ids      = aws_subnet.private[*].id
  instance_types  = var.node_instance_types

  scaling_config {
    desired_size = var.node_desired
    min_size     = var.node_min
    max_size     = var.node_max
  }

  update_config {
    max_unavailable = 1
  }

  labels = {
    role = "default"
  }

  depends_on = [
    aws_iam_role_policy_attachment.eks_worker_node_policy,
    aws_iam_role_policy_attachment.eks_cni_policy,
    aws_iam_role_policy_attachment.eks_ecr_readonly,
  ]

  tags = {
    Name = "${var.cluster_name}-default-node"
  }
}

# ---------------------------------------------------------------------------
# ECR repositories for autosre images
# ---------------------------------------------------------------------------
resource "aws_ecr_repository" "agent" {
  name                 = "${var.cluster_name}/agent"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }
}

resource "aws_ecr_repository" "diagnoser" {
  name                 = "${var.cluster_name}/diagnoser"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }
}

resource "aws_ecr_repository" "learner" {
  name                 = "${var.cluster_name}/learner"
  image_tag_mutability = "MUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }
}

# ---------------------------------------------------------------------------
# aws-auth ConfigMap — grant node role access to the cluster
# ---------------------------------------------------------------------------
resource "kubernetes_config_map_v1_data" "aws_auth" {
  metadata {
    name      = "aws-auth"
    namespace = "kube-system"
  }

  data = {
    mapRoles = yamlencode([
      {
        rolearn  = aws_iam_role.eks_node.arn
        username = "system:node:{{EC2PrivateDNSName}}"
        groups   = ["system:bootstrappers", "system:nodes"]
      }
    ])
  }

  force = true

  depends_on = [aws_eks_cluster.this]
}

# ---------------------------------------------------------------------------
# Expose cluster info
# ---------------------------------------------------------------------------
locals {
  module = {
    eks = {
      cluster_endpoint                    = aws_eks_cluster.this.endpoint
      cluster_certificate_authority_data  = aws_eks_cluster.this.certificate_authority[0].data
      cluster_name                        = aws_eks_cluster.this.name
      cluster_oidc_issuer_url             = aws_eks_cluster.this.identity[0].oidc[0].issuer
      oidc_provider_arn                   = aws_iam_openid_connect_provider.eks.arn
    }
  }
}
