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
output "subscription_id" {
  value       = google_pubsub_subscription.log_monitor_subscription.name
  description = "The Pub/Sub subscription ID the Log Monitor pulls from (passed to Helm as logMonitor.subscriptionId)."
}

output "log_keyword" {
  value       = var.logging_sink_custom_keyword
  description = "The keyword the logging sink filters on (passed to Helm as logMonitor.logKeyword so the two stay in step)."
}
