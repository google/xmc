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

variable "k8s_namespace" {
  type        = string
  description = "The Kubernetes namespace where the XMC system components are deployed."
  default     = "xmc"
}

variable "actuator_ksa_name" {
  type        = string
  description = "The name of the Kubernetes Service Account for the Actuator."
  default     = "actuator-service-account"
}

variable "reconciler_ksa_name" {
  type        = string
  description = "The name of the Kubernetes Service Account for the Reconciler."
  default     = "reconciler-service-account"
}

variable "ess_ksa_name" {
  type        = string
  description = "The name of the Kubernetes Service Account for the ESS."
  default     = "experiment-server"
}

variable "xid_ksa_name" {
  type        = string
  description = "The name of the Kubernetes Service Account for the XID service."
  default     = "xid-service"
}

variable "environment" {
  type        = string
  description = "The environment name."
}

variable "log_monitor_principal" {
  type        = string
  description = "The principal of Log Monitor Google Service Account."
}

variable "ess_gsa_name" {
  type        = string
  description = "The name of the OIDC service account to use for Redis tasks."
  default     = "xmc-ess-sa"
}
