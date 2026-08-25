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
variable "project_id" {
  type        = string
  description = "The GCP project ID that owns the metrics topic."
}

variable "message_bus" {
  type        = string
  description = "The message bus the ingestion gateway publishes to. This module provisions resources only when it is \"pubsub\"."
  default     = "redpanda"
  validation {
    condition     = contains(["redpanda", "pubsub"], var.message_bus)
    error_message = "message_bus must be either \"redpanda\" or \"pubsub\"."
  }
}

variable "metrics_enabled" {
  type        = bool
  description = "Whether the metrics pipeline, and therefore the ingestion gateway, is deployed at all."
  default     = false
}

variable "topic_name" {
  type        = string
  description = "The Pub/Sub topic ID the ingestion gateway publishes to. Must match the topic name passed to the gateway with -topic_name."
  default     = "xmc-metrics"
}

variable "publisher_service_account_email" {
  type        = string
  description = "Email of the pre-existing service account the ingestion gateway impersonates through Workload Identity."
}

variable "allowed_storage_regions" {
  type        = list(string)
  description = "Regions the topic may persist messages in. Leave empty to accept the Google-managed default policy."
  default     = []
}

variable "message_retention_duration" {
  type        = string
  description = "How long the topic retains unacknowledged messages, as a duration string such as \"86400s\". Null leaves the Pub/Sub default in place."
  default     = null
}
