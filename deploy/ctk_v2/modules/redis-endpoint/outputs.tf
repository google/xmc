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

# Reading through terraform_data rather than the local keeps the precondition load-bearing: the
# address cannot be consumed by the Helm release without the guard having been evaluated first.
output "redis_addr" {
  value       = terraform_data.resolved_endpoint.output
  description = "host:port of the Redis instance the application should connect to"
}

output "redis_auth_string" {
  value       = local.auth_string
  description = "AUTH string for the resolved Redis instance, empty when AUTH is disabled"
  sensitive   = true
}
