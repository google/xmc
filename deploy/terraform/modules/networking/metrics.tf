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
# VPC Peering & Security Setup for XMC Metrics (Redpanda & ClickHouse)
# -----------------------------------------------------------------------------

locals {
  metrics_networking_enabled = var.enable_metrics_networking
  has_redpanda_vpc           = var.byo_redpanda_vpc_id != "" && var.byo_redpanda_vpc_id != null
  has_clickhouse_vpc         = var.byo_clickhouse_vpc_id != "" && var.byo_clickhouse_vpc_id != null
  has_xmc_vpc                = var.xmc_vpc_network_id != "" && var.xmc_vpc_network_id != null

  peer_xmc_redpanda        = local.metrics_networking_enabled && local.has_xmc_vpc && local.has_redpanda_vpc
  peer_clickhouse_redpanda = local.metrics_networking_enabled && local.has_clickhouse_vpc && local.has_redpanda_vpc

  redpanda_project_id = local.has_redpanda_vpc && length(regexall("projects/([^/]+)/", var.byo_redpanda_vpc_id)) > 0 ? regex("projects/([^/]+)/", var.byo_redpanda_vpc_id)[0] : var.project_id
}

# -----------------------------------------------------------------------------
# VPC Peering: XMC Application VPC <-> External BYO Redpanda VPC
# -----------------------------------------------------------------------------
resource "google_compute_network_peering" "xmc_to_redpanda" {
  count        = local.peer_xmc_redpanda ? 1 : 0
  name         = "xmc-to-redpanda-peering-${var.environment}"
  network      = var.xmc_vpc_network_id
  peer_network = var.byo_redpanda_vpc_id
}

resource "google_compute_network_peering" "redpanda_to_xmc" {
  count        = local.peer_xmc_redpanda ? 1 : 0
  name         = "redpanda-to-xmc-peering-${var.environment}"
  network      = var.byo_redpanda_vpc_id
  peer_network = var.xmc_vpc_network_id
}

# -----------------------------------------------------------------------------
# VPC Peering: External BYO ClickHouse VPC <-> External BYO Redpanda VPC
# -----------------------------------------------------------------------------
resource "google_compute_network_peering" "clickhouse_to_redpanda" {
  count        = local.peer_clickhouse_redpanda ? 1 : 0
  name         = "clickhouse-to-redpanda-peering-${var.environment}"
  network      = var.byo_clickhouse_vpc_id
  peer_network = var.byo_redpanda_vpc_id
}

resource "google_compute_network_peering" "redpanda_to_clickhouse" {
  count        = local.peer_clickhouse_redpanda ? 1 : 0
  name         = "redpanda-to-clickhouse-peering-${var.environment}"
  network      = var.byo_redpanda_vpc_id
  peer_network = var.byo_clickhouse_vpc_id
}

# -----------------------------------------------------------------------------
# Firewall Rules: Allow Gateway -> Redpanda (TCP 9092, 9094, 8081)
# -----------------------------------------------------------------------------
resource "google_compute_firewall" "allow_gateway_to_redpanda" {
  count     = local.metrics_networking_enabled && local.has_redpanda_vpc && length(var.gateway_source_cidrs) > 0 ? 1 : 0
  name      = "xmc-allow-gateway-to-redpanda-${var.environment}"
  project   = local.redpanda_project_id
  network   = var.byo_redpanda_vpc_id
  direction = "INGRESS"
  priority  = 1000

  allow {
    protocol = "tcp"
    ports    = ["9092", "9094", "8081"]
  }

  source_ranges = var.gateway_source_cidrs
  description   = "Allow TCP ingress on ports 9092/9094/8081 from Gateway to Redpanda."
}

# -----------------------------------------------------------------------------
# Firewall Rules: Allow ClickHouse -> Redpanda (TCP 9092, 9094, 8081)
# -----------------------------------------------------------------------------
resource "google_compute_firewall" "allow_clickhouse_to_redpanda" {
  count     = local.metrics_networking_enabled && local.has_redpanda_vpc && length(var.clickhouse_source_cidrs) > 0 ? 1 : 0
  name      = "xmc-allow-clickhouse-to-redpanda-${var.environment}"
  project   = local.redpanda_project_id
  network   = var.byo_redpanda_vpc_id
  direction = "INGRESS"
  priority  = 1000

  allow {
    protocol = "tcp"
    ports    = ["9092", "9094", "8081"]
  }

  source_ranges = var.clickhouse_source_cidrs
  description   = "Allow TCP ingress on ports 9092/9094/8081 from ClickHouse to Redpanda."
}
