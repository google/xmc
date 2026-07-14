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

variable "ui_gsa_name" {
  type        = string
  default     = "ui-service-account"
  description = "Name of the service account for the UI Cloud Run service."
}

variable "ui_image_url" {
  type        = string
  description = "Docker image URL for the UI Cloud Run service."
}

variable "api_url" {
  type        = string
  description = "The URL of the API that the UI will connect to."
}

variable "ui_service_location" {
  type        = string
  default     = "europe-west1"
  description = "The GCP region where UI Cloud Run service is deployed."
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

variable "refresh_interval_ms" {
  description = "The refresh interval in milliseconds for the UI."
  type        = string
  default     = "30000"
}


variable "enable_status_message_history" {
  description = "If true, the UI will display status message history tab."
  type        = bool
  default     = false
}
