#!/bin/sh
# Dynamically generate config.js from environment variables at startup

CONFIG_FILE="/usr/share/nginx/html/config.js"

echo "Generating runtime config.js at ${CONFIG_FILE}..."

# Set default values if not specified
API_URL="${XMC_API_URL:-https://api.example.com/v1}"
REFRESH_MS="${XMC_REFRESH_INTERVAL_MS:-30000}"
STATUS_HISTORY="${XMC_STATUS_MESSAGE_HISTORY_ENABLED:-false}"

cat <<EOF > "${CONFIG_FILE}"
window.XMC_API_URL = "${API_URL}";
window.XMC_REFRESH_INTERVAL_MS = ${REFRESH_MS};
window.XMC_FEATURE_FLAGS = {
  "statusMessageHistoryEnabled": ${STATUS_HISTORY}
};
EOF

echo "Config.js generated successfully:"
cat "${CONFIG_FILE}"
