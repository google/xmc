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
  description = "The GCP project ID where the central DNS zone will be created (e.g., xmc-control-1)."
}

variable "domain_name" {
  type        = string
  description = "The central domain name under which dev environments will have their A records (e.g., dev.example.com)."
}

variable "managed_zone_name" {
  type        = string
  description = "The name of the managed zone in Cloud DNS (e.g., xmc-central-zone)."
  default     = "xmc-central-zone"
}
