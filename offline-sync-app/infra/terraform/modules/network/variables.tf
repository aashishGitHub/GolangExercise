variable "name_prefix" {
  type        = string
  description = "Prefix for resource names, e.g. \"fieldsync-prod\""
}

variable "vpc_cidr" {
  type    = string
  default = "10.42.0.0/16"
}

variable "azs" {
  type        = list(string)
  description = "Two AZs is enough for Aurora Serverless v2 + RDS Proxy HA without over-provisioning NAT costs"
  default     = ["us-east-1a", "us-east-1b"]
}
