# Copyright 2026 Google LLC
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

variable "enabled" {
  type        = bool
  description = "Whether to install the ML Diagnostics Connection Operator Helm chart."
  default     = false
}

variable "enable_managed_mldiagnostics" {
  type        = bool
  description = "Whether GKE Managed ML Diagnostics is enabled on the target cluster (mutually exclusive with enabled)."
  default     = false
}

variable "release_name" {
  type        = string
  description = "Name of the Helm release for the ML Diagnostics Connection Operator."
  default     = "mldiagnostics-connection-operator"
}

variable "chart_name" {
  type        = string
  description = "OCI chart reference for the ML Diagnostics Connection Operator."
  default     = "oci://us-docker.pkg.dev/ai-on-gke/mldiagnostics-webhook-and-operator-helm/mldiagnostics-connection-operator"
}

variable "chart_version" {
  type        = string
  description = "Pinned semantic version of the ML Diagnostics Connection Operator Helm chart."
  default     = "0.24.0"

  validation {
    condition     = trimspace(var.chart_version) != "" && lower(trimspace(var.chart_version)) != "latest"
    error_message = "chart_version must be a non-empty pinned semantic version (for example '0.24.0'), never 'latest'."
  }
}

variable "namespace" {
  type        = string
  description = "Kubernetes namespace in which to install the ML Diagnostics Connection Operator."
  default     = "gke-mldiagnostics"
}

variable "create_namespace" {
  type        = bool
  description = "Whether to create the target Kubernetes namespace if it does not exist."
  default     = true
}

variable "timeout" {
  type        = number
  description = "Timeout in seconds to wait for the Helm release operation."
  default     = 300
}

variable "wait" {
  type        = bool
  description = "Whether to wait until all Helm release resources reach a ready state."
  default     = true
}

variable "dependencies" {
  type        = list(string)
  description = "Upstream release IDs that must complete before installing the operator."
  default     = []
}

variable "cluster_id" {
  type        = string
  description = "Full resource ID of the target GKE cluster (projects/PROJECT_ID/locations/LOCATION/clusters/CLUSTER_NAME)."
}

variable "cluster_endpoint" {
  type        = string
  description = "Control-plane endpoint of the target GKE cluster without https:// prefix."
  default     = null
}

variable "cluster_ca_certificate" {
  type        = string
  description = "Base64-encoded public CA certificate of the target GKE cluster."
  default     = null
}

variable "access_token" {
  type        = string
  description = "OAuth2 access token used to authenticate with the target GKE cluster."
  default     = null
}
