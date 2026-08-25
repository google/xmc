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
# -----------------------------------------------------------------------------
# Destination topic for the XMC ingestion gateway when it runs with
# -message_bus=pubsub, plus the IAM the gateway needs to reach it.
#
# The gateway is the only publisher. Nothing here creates a subscription: XMC has
# no Pub/Sub consumer, because ClickHouse ingests the metrics topic through its
# native ENGINE = Kafka table and has no Pub/Sub equivalent. Attach your own
# subscriber, and create it with enable_message_ordering so the per-work-unit
# ordering the gateway publishes with survives delivery.
#
# pubsub.googleapis.com is enabled by the caller. In the Cluster Toolkit blueprint
# that is the service-enablement module in the apis group.
# -----------------------------------------------------------------------------

locals {
  # In redpanda mode the gateway never talks to Pub/Sub, so this module provisions
  # nothing. The blueprint has no conditionals, so the decision is made here.
  create = var.metrics_enabled && var.message_bus == "pubsub"
}

resource "google_pubsub_topic" "metrics" {
  count                      = local.create ? 1 : 0
  project                    = var.project_id
  name                       = var.topic_name
  message_retention_duration = var.message_retention_duration

  dynamic "message_storage_policy" {
    for_each = length(var.allowed_storage_regions) > 0 ? [1] : []
    content {
      allowed_persistence_regions = var.allowed_storage_regions
    }
  }
}

resource "google_pubsub_topic_iam_member" "gateway_publisher" {
  count   = local.create ? 1 : 0
  project = var.project_id
  topic   = google_pubsub_topic.metrics[0].name
  role    = "roles/pubsub.publisher"
  member  = "serviceAccount:${var.publisher_service_account_email}"
}

# roles/pubsub.publisher grants pubsub.topics.publish and nothing else. The gateway
# also calls Topic.Exists at startup, which needs pubsub.topics.get, so without this
# second binding it fails to start against a topic it could otherwise publish to.
resource "google_pubsub_topic_iam_member" "gateway_viewer" {
  count   = local.create ? 1 : 0
  project = var.project_id
  topic   = google_pubsub_topic.metrics[0].name
  role    = "roles/pubsub.viewer"
  member  = "serviceAccount:${var.publisher_service_account_email}"
}
