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

variable "cluster_name" {
  description = "The name for the GKE cluster."
  type        = string
  default     = "xmc-cluster"
}

variable "gke_nodes_sa_account_id" {
  description = "The account id for the GKE nodes service account."
  type        = string
  default     = "xmc-gke-nodes"
}

variable "region" {
  description = "The region to host the cluster in."
  type        = string
  default     = "us-central1"
}

variable "machine_type" {
  description = "The machine type for cluster nodes."
  type        = string
  default     = "e2-standard-4"
}

variable "max_node_count" {
  description = "The maximum number of nodes in the node pool, per zone."
  type        = number
  default     = 2 # 2 per zone * 3 zones = 6 nodes total.
}

variable "environment" {
  type        = string
  description = "The environment name."
}

variable "is_dev_environment" {
  description = "If true, deletion protection will be disabled on created resources."
  type        = bool
  default     = false
}

variable "enable_managed_mldiagnostics" {
  description = "If true, the needed artifacts for ML Diagnostics on-demand profiling will be enabled or deployed. https://docs.cloud.google.com/tpu/docs/ml-diagnostics/gke "
  type        = bool
  default     = false
}
