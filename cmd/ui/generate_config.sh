#!/bin/sh
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

# Dynamically generate config.js from environment variables at startup.
set -eu

CONFIG_FILE="${CONFIG_FILE:-/tmp/config.js}"

get_bool() {
  case "$(echo "${1:-false}" | tr '[:upper:]' '[:lower:]')" in
    true|1|yes) echo "true" ;;
    *) echo "false" ;;
  esac
}

IS_NON_PROD_AUTH="false"
if [ "$(get_bool "${XMC_NON_PROD_AUTH:-}")" = "true" ] || [ "$(get_bool "${SHOW_SAFETY_WARNING:-}")" = "true" ]; then
  IS_NON_PROD_AUTH="true"
fi

cat <<EOF > "${CONFIG_FILE}"
window.XMC_API_URL = "${XMC_API_URL:-https://api.example.com/v1}";
window.XMC_REFRESH_INTERVAL_MS = ${XMC_REFRESH_INTERVAL_MS:-30000};
window.XMC_FEATURE_FLAGS = {
  "adminPanelEnabled": $(get_bool "${XMC_ADMIN_PANEL_ENABLED:-}"),
  "resourceDashboardEnabled": $(get_bool "${XMC_RESOURCE_DASHBOARD_ENABLED:-}"),
  "performanceDashboardEnabled": $(get_bool "${XMC_PERFORMANCE_DASHBOARD_ENABLED:-}"),
  "isNonProdAuth": ${IS_NON_PROD_AUTH},
  "pauseSchedulingEnabled": $(get_bool "${pause_scheduling_enabled:-}")
};
EOF
