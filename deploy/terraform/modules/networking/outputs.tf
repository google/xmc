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
output "actuator_static_ip_address" {
  value       = google_compute_global_address.actuator_static_ip.address
  description = "The static external IP address for the Actuator service."
}

output "ess_http_static_ip_address" {
  value       = google_compute_global_address.ess_http_static_ip.address
  description = "The static external IP address for the ESS HTTP service."
}

output "ess_grpc_static_ip_address" {
  value       = google_compute_global_address.ess_grpc_static_ip.address
  description = "The static external IP address for the ESS gRPC service."
}

output "ui_static_ip_address" {
  value       = google_compute_global_address.ui_static_ip.address
  description = "The static external IP address for the UI load balancer."
}

output "ui_url" {
  description = "The URL of the XMC UI."
  value       = "https://ui.${var.domain_name}"
}

output "xmc_to_redpanda_peering_id" {
  description = "The ID of the VPC peering connection from XMC VPC to Redpanda VPC."
  value       = length(google_compute_network_peering.xmc_to_redpanda) > 0 ? google_compute_network_peering.xmc_to_redpanda[0].id : null
}

output "clickhouse_to_redpanda_peering_id" {
  description = "The ID of the VPC peering connection from ClickHouse VPC to Redpanda VPC."
  value       = length(google_compute_network_peering.clickhouse_to_redpanda) > 0 ? google_compute_network_peering.clickhouse_to_redpanda[0].id : null
}

