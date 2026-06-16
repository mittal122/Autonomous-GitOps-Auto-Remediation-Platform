output "cluster_name" {
  description = "EKS cluster name."
  value       = aws_eks_cluster.this.name
}

output "cluster_endpoint" {
  description = "EKS API server endpoint."
  value       = aws_eks_cluster.this.endpoint
}

output "cluster_certificate_authority_data" {
  description = "Base64-encoded cluster CA certificate."
  value       = aws_eks_cluster.this.certificate_authority[0].data
  sensitive   = true
}

output "cluster_oidc_issuer_url" {
  description = "OIDC issuer URL — used to configure IRSA."
  value       = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

output "oidc_provider_arn" {
  description = "ARN of the IAM OIDC provider for IRSA."
  value       = aws_iam_openid_connect_provider.eks.arn
}

output "autosre_agent_role_arn" {
  description = "ARN of the IRSA role for the autosre agent service account."
  value       = aws_iam_role.autosre_agent.arn
}

output "ecr_agent_url" {
  description = "ECR URL for the autosre agent image."
  value       = aws_ecr_repository.agent.repository_url
}

output "ecr_diagnoser_url" {
  description = "ECR URL for the diagnoser image."
  value       = aws_ecr_repository.diagnoser.repository_url
}

output "ecr_learner_url" {
  description = "ECR URL for the learner image."
  value       = aws_ecr_repository.learner.repository_url
}

output "kubeconfig_command" {
  description = "Run this command to update your local kubeconfig."
  value       = "aws eks update-kubeconfig --region ${var.aws_region} --name ${aws_eks_cluster.this.name}"
}

output "vpc_id" {
  description = "VPC ID."
  value       = aws_vpc.this.id
}

output "private_subnet_ids" {
  description = "IDs of the private subnets used by node groups."
  value       = aws_subnet.private[*].id
}
