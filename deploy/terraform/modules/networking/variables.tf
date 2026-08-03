# Copyright 2025 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
variable "project_id" {
  type        = string
  description = "The GCP project ID where the resources will be created."
}

variable "iap_client_id" {
  description = "OAuth Client ID for IAP"
  type        = string
  sensitive   = true
}

variable "iap_client_secret" {
  description = "OAuth Client Secret for IAP"
  type        = string
  sensitive   = true
}

variable "allowed_users" {
  description = "List of users/groups allowed to access the UI (e.g., user:jane@example.com)"
  type        = list(string)
}

variable "domain_name" {
  type        = string
  description = "The domain name of the UI Cloud Run service."
}

variable "ui_service_name" {
  type        = string
  description = "The name of the UI Cloud Run service."
}

variable "ui_service_location" {
  type        = string
  default     = "europe-west1"
  description = "The location of the UI Cloud Run service."
}

variable "environment" {
  type        = string
  description = "The environment name."
}

variable "enable_metrics_networking" {
  type        = bool
  default     = false
  description = "Whether to enable VPC peering and firewall rules for metrics services (Redpanda and ClickHouse)."
}

variable "xmc_vpc_network_id" {
  type        = string
  default     = ""
  description = "The ID or self link of the XMC Application VPC network."
}

variable "byo_redpanda_vpc_id" {
  type        = string
  default     = ""
  description = "The ID or self link of the external BYO Redpanda VPC network."
}

variable "byo_clickhouse_vpc_id" {
  type        = string
  default     = ""
  description = "The ID or self link of the external BYO ClickHouse VPC network."
}

variable "gateway_source_cidrs" {
  type        = list(string)
  default     = []
  description = "List of CIDR ranges for XMC Gateway allowed to communicate with Redpanda."
}

variable "clickhouse_source_cidrs" {
  type        = list(string)
  default     = []
  description = "List of CIDR ranges for ClickHouse allowed to communicate with Redpanda."
}

