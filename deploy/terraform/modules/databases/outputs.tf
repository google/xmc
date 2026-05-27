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
output "spanner_instance_name" {
  description = "The name of the main Spanner instance."
  value       = google_spanner_instance.main_instance.name
}

output "experiment_database_name" {
  description = "The name of the experiment Spanner database."
  value       = google_spanner_database.experiment_database.name
}

output "xid_database_name" {
  description = "The name of the XID Spanner database."
  value       = google_spanner_database.xid_database.name
}