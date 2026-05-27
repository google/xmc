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
output "name_servers" {
  description = "The name servers for the central managed zone. Add these as NS records in the parent domain (e.g., Zoneman)."
  value       = google_dns_managed_zone.xmc_central_managed_zone.name_servers
}

output "managed_zone_name" {
  description = "The name of the newly created managed zone. Provide this to the dev environments."
  value       = google_dns_managed_zone.xmc_central_managed_zone.name
}

output "control_project_id" {
  description = "The project ID containing the central DNS zone. Provide this to the dev environments."
  value       = google_project_service.dns_api.project
}
