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

variable "provisioned_host" {
  description = "Host of the Memorystore instance the blueprint provisioned. Null when deploy_redis is false."
  type        = string
  default     = null
}

variable "provisioned_port" {
  description = "Port of the Memorystore instance the blueprint provisioned. Null when deploy_redis is false."
  type        = string
  default     = null
}

variable "provisioned_auth_string" {
  description = "AUTH string of the Memorystore instance the blueprint provisioned. Null when deploy_redis is false."
  type        = string
  default     = null
  sensitive   = true
}

variable "external_host" {
  description = "Host of a Redis instance supplied by the operator. Overrides the provisioned instance when set."
  type        = string
  default     = ""
}

variable "external_port" {
  description = "Port of the operator-supplied Redis instance. Read only when external_host is set."
  type        = string
  default     = "6379"
}

variable "external_auth_string" {
  description = "AUTH string of the operator-supplied Redis instance. Empty when the instance has AUTH disabled."
  type        = string
  default     = ""
  sensitive   = true
}
