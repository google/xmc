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

# Dynamically generate config.js from environment variables at startup

CONFIG_FILE="${CONFIG_FILE:-/tmp/config.js}"

echo "Generating runtime config.js at ${CONFIG_FILE}..."

# Helper to parse boolean values to lowercase 'true' or 'false'
get_bool_env() {
  case "$(echo "${1:-$2}" | tr '[:upper:]' '[:lower:]')" in
    true|1|yes) echo "true" ;;
    *) echo "false" ;;
  esac
}

# Set default values if not specified
API_URL="${XMC_API_URL:-https://api.example.com/v1}"
REFRESH_MS="${XMC_REFRESH_INTERVAL_MS:-30000}"
STATUS_HISTORY=$(get_bool_env "$XMC_STATUS_MESSAGE_HISTORY_ENABLED" true)
ADMIN_PANEL=$(get_bool_env "$XMC_ADMIN_PANEL_ENABLED" false)
RESOURCE_DASHBOARD=$(get_bool_env "$XMC_RESOURCE_DASHBOARD_ENABLED" false)
PERFORMANCE_DASHBOARD=$(get_bool_env "$XMC_PERFORMANCE_DASHBOARD_ENABLED" false)
NON_PROD_AUTH="false"
if [ "$(get_bool_env "$XMC_NON_PROD_AUTH" false)" = "true" ] || [ "$(get_bool_env "$SHOW_SAFETY_WARNING" false)" = "true" ]; then
  NON_PROD_AUTH="true"
fi

cat <<EOF > "${CONFIG_FILE}"
window.XMC_API_URL = "${API_URL}";
window.XMC_REFRESH_INTERVAL_MS = ${REFRESH_MS};
window.XMC_FEATURE_FLAGS = {
  "statusMessageHistoryEnabled": ${STATUS_HISTORY},
  "adminPanelEnabled": ${ADMIN_PANEL},
  "resourceDashboardEnabled": ${RESOURCE_DASHBOARD},
  "performanceDashboardEnabled": ${PERFORMANCE_DASHBOARD},
  "isNonProdAuth": ${NON_PROD_AUTH}
};
EOF

# For backward compatibility, also mirror config.js to web root if writable
if [ -w "/usr/share/nginx/html" ] && [ "${CONFIG_FILE}" != "/usr/share/nginx/html/config.js" ]; then
  cp "${CONFIG_FILE}" /usr/share/nginx/html/config.js 2>/dev/null || true
fi

echo "Config.js generated successfully:"
cat "${CONFIG_FILE}"
