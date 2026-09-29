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

variable "service_account_email" {
  type        = string
  description = "The email of the pre-existing service account the Log Monitor workload runs as; it is granted subscriber access to the subscription."
}

variable "pubsub_topic_name" {
  type        = string
  description = "The name of the Pub/Sub topic for xmc logs."
  default     = "xmc-log-monitor-topic"
}

variable "pubsub_subscription_name" {
  type        = string
  description = "The name of the Pub/Sub subscription to create."
  default     = "xmc-log-monitor-subscription"
}

variable "logging_sink_name" {
  type        = string
  description = "The name of the logging sink for xmc logs."
  default     = "xmc-log-monitor-sink"
}

variable "logging_sink_custom_keyword" {
  type        = string
  description = "The keyword to additionally forward logs with."
  default     = "XMC LOG:"
}

variable "environment" {
  type        = string
  description = "The environment name."
}
