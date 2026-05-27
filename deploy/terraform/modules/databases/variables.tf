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

variable "spanner_instance_config" {
  description = "The Spanner instance config, e.g., a regional or multi-regional location."
  type        = string
  default     = "nam3"
}

variable "spanner_instance_name" {
  description = "The name for the main Spanner instance."
  type        = string
  default     = "xmc"
}

variable "spanner_experiment_database_name" {
  description = "The name for the experiment Spanner database."
  type        = string
  default     = "experiment-db"
}

variable "spanner_xid_database_name" {
  description = "The name for the XID Spanner database."
  type        = string
  default     = "xids-db"
}

variable "spanner_processing_units" {
  description = "Processing units for the Spanner instance. 100 PUs = 0.1 nodes."
  type        = number
  default     = 100
}

variable "actuator_principal" {
  type        = string
  description = "The principal of the Actuator GSA to grant Spanner access."
}

variable "reconciler_principal" {
  type        = string
  description = "The principal of the Reconciler KSA to grant Spanner access."
}

variable "ess_principal" {
  type        = string
  description = "The principal of the ESS GSA to grant Spanner access."
}

variable "log_monitor_principal" {
  type        = string
  description = "The principal of log monitor service account to grant Spanner access."
  default     = null
}

variable "xid_principal" {
  type        = string
  description = "The principal of the XID Service GSA to grant Spanner access."
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
