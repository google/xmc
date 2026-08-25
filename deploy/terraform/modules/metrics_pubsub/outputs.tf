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
output "topic_name" {
  description = "The metrics topic name to pass to the ingestion gateway. Falls back to the configured name in redpanda mode, where this module creates nothing but the gateway still needs a topic name."
  value       = local.create ? google_pubsub_topic.metrics[0].name : var.topic_name
}

output "topic_id" {
  description = "Fully qualified ID of the created topic, or null in redpanda mode."
  value       = local.create ? google_pubsub_topic.metrics[0].id : null
}

output "enabled" {
  description = "Whether this module provisioned a Pub/Sub topic."
  value       = local.create
}
