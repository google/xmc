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

locals {
  ar_matches = regexall(
    "^([a-z0-9-]+)-docker\\.pkg\\.dev/([^/]+)/([^/]+)(?:/.*)?$",
    trimspace(var.image_registry),
  )
  is_artifact_registry = length(local.ar_matches) == 1

  repository_location   = local.is_artifact_registry ? local.ar_matches[0][0] : ""
  repository_project_id = local.is_artifact_registry ? local.ar_matches[0][1] : ""
  repository_id         = local.is_artifact_registry ? local.ar_matches[0][2] : ""

  reader_sa_emails = toset(compact([for sa in var.reader_service_accounts : trimspace(sa)]))
}

resource "google_artifact_registry_repository_iam_member" "node_pool_reader" {
  count      = local.is_artifact_registry ? 1 : 0
  project    = local.repository_project_id
  location   = local.repository_location
  repository = local.repository_id
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${var.node_service_account_email}"
}

resource "google_artifact_registry_repository_iam_member" "additional_readers" {
  for_each   = local.is_artifact_registry ? local.reader_sa_emails : toset([])
  project    = local.repository_project_id
  location   = local.repository_location
  repository = local.repository_id
  role       = "roles/artifactregistry.reader"
  member     = "serviceAccount:${each.value}"
}
