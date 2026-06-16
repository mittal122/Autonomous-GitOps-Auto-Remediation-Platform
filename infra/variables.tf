variable "aws_region" {
  description = "AWS region to deploy the EKS cluster."
  type        = string
  default     = "us-east-1"
}

variable "cluster_name" {
  description = "Name for the EKS cluster and all derived resource names."
  type        = string
  default     = "autosre"
}

variable "cluster_version" {
  description = "Kubernetes version for the EKS control plane."
  type        = string
  default     = "1.29"
}

variable "vpc_cidr" {
  description = "CIDR block for the VPC."
  type        = string
  default     = "10.0.0.0/16"
}

variable "availability_zones" {
  description = "List of AZs to spread the node groups across (min 2)."
  type        = list(string)
  default     = ["us-east-1a", "us-east-1b", "us-east-1c"]
}

variable "node_instance_types" {
  description = "EC2 instance types for the default managed node group."
  type        = list(string)
  default     = ["t3.medium"]
}

variable "node_desired" {
  description = "Desired node count in the default node group."
  type        = number
  default     = 2
}

variable "node_min" {
  description = "Minimum node count in the default node group."
  type        = number
  default     = 1
}

variable "node_max" {
  description = "Maximum node count in the default node group."
  type        = number
  default     = 5
}

variable "autosre_image" {
  description = "Container image for the autosre agent (pushed to ECR)."
  type        = string
  default     = ""
}

variable "diagnoser_image" {
  description = "Container image for the diagnoser service."
  type        = string
  default     = ""
}

variable "learner_image" {
  description = "Container image for the learner service."
  type        = string
  default     = ""
}

variable "tags" {
  description = "Tags to apply to all created AWS resources."
  type        = map(string)
  default = {
    Project   = "autosre"
    ManagedBy = "terraform"
  }
}
