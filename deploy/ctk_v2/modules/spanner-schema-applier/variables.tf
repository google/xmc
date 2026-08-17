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

variable "project_id" {
  type        = string
  description = "GCP Project ID"
}

variable "instance_name" {
  type        = string
  description = "Spanner Instance Name"
}

variable "database_name" {
  type        = string
  description = "Spanner Database Name"
}

variable "schema_file" {
  type        = string
  description = "Path to the schema SQL file"
}



variable "db_type" {
  type        = string
  description = "Database type (e.g. ess or xid)"
}

variable "depends_on_anchor" {
  type        = any
  default     = null
  description = "Unused. Point this at another module's output to order this module after it."
}
