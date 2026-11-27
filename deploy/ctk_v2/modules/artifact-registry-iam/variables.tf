# Copyright 2026 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

variable "image_registry" {
  description = "Target Artifact Registry Docker repository URI (<LOCATION>-docker.pkg.dev/<PROJECT_ID>/<REPO>)."
  type        = string
  nullable    = false

  validation {
    condition = can(regex(
      "^[a-z0-9-]+-docker\\.pkg\\.dev/[^/]+/[^/]+(/.*)?$",
      trimspace(var.image_registry),
    ))
    error_message = "image_registry must be a non-empty Artifact Registry Docker URI '<LOCATION>-docker.pkg.dev/<PROJECT_ID>/<REPO>'."
  }
}

variable "node_service_account_email" {
  description = "Cluster node pool GCP service account email granted Artifact Registry repository reader access."
  type        = string
  nullable    = false
}

variable "reader_service_accounts" {
  description = "Additional custom GCP service account emails (e.g. MultiKueue worker or GKE Autopilot custom node service accounts) granted Artifact Registry repository reader access."
  type        = list(string)
  default     = []
  nullable    = false

  validation {
    condition = alltrue([
      for sa in var.reader_service_accounts :
      trimspace(sa) != "" &&
      can(regex("^[^:\\s]+@[^\\s]+\\.gserviceaccount\\.com$", trimspace(sa))) &&
      !endswith(trimspace(sa), "-compute@developer.gserviceaccount.com")
    ])
    error_message = "Each entry in reader_service_accounts must be a non-empty bare custom GCP service account email (without 'serviceAccount:') and must not be a Compute Engine default service account ('*-compute@developer.gserviceaccount.com')."
  }
}
