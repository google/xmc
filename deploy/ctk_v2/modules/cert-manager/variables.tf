variable "project_id" {
  type        = string
  description = "GCP Project ID"
}

variable "environment" {
  type        = string
  description = "Environment name (e.g. xmcv2)"
}

variable "domain_name" {
  type        = string
  description = "Base domain name (e.g. example.com)"
}

variable "domains" {
  type        = list(string)
  description = "List of domains to issue the certificate for"
}
