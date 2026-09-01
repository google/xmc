/**
 * Copyright 2026 Google LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

# Cluster Toolkit blueprints have no conditionals, so the choice between the Memorystore instance
# the blueprint provisions and one the operator brings cannot be expressed where it is made. This
# module is where it lands instead: it takes both candidates and resolves the single address the
# chart is given.

locals {
  # An operator-supplied host wins over anything the blueprint provisioned. A Memorystore instance
  # is reachable only from its one authorized network and PSA peering is not transitive, so a
  # cluster on a pre-existing VPC cannot route to an instance the blueprint created on a new one.
  # Overriding has to be possible without first tearing that instance down.
  use_external = var.external_host != ""

  # The redis module returns null for every output when deploy_redis is false. Normalising to ""
  # keeps the precondition below a single comparison rather than a null check per field.
  provisioned_host        = var.provisioned_host == null ? "" : var.provisioned_host
  provisioned_port        = var.provisioned_port == null ? "" : var.provisioned_port
  provisioned_auth_string = var.provisioned_auth_string == null ? "" : var.provisioned_auth_string

  host        = local.use_external ? var.external_host : local.provisioned_host
  port        = local.use_external ? var.external_port : local.provisioned_port
  auth_string = local.use_external ? var.external_auth_string : local.provisioned_auth_string

  redis_addr = "${local.host}:${local.port}"
}

# Setting deploy_redis to false without supplying an external host used to render the chart with
# redisAddr of ":" -- a deployment that installs cleanly and then fails at runtime, in every
# service that reaches the queue. Failing the plan turns that into an error at the point the
# mistake was made.
resource "terraform_data" "resolved_endpoint" {
  input = local.redis_addr

  lifecycle {
    precondition {
      condition     = local.host != "" && local.port != ""
      error_message = <<-EOT
        No Redis endpoint resolved. XMC requires one: the actuator, the experiment state server
        and the health service all read redisAddr.

        Either set deploy_redis to true to provision a Memorystore instance, or set
        external_redis_host (and external_redis_port, external_redis_auth_string) to an instance
        reachable from the cluster's network.
      EOT
    }
  }
}
