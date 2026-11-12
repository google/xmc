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

"""CSS selector constants for XMC Playwright UI E2E verifiers."""

DASHBOARD_ROOT_SEL, WIP_BANNER_SEL = "xmc-resource-dashboard", "xmc-wip-banner"
QUEUE_CARD_SEL, RESOURCE_OPTION_SEL = '[data-testid="queue-card"]', "mat-option"
QUEUE_TITLE_SEL, JOB_POSITION_CELL_SEL = (
    '[data-testid="queue-title"]',
    '[data-testid="job-position-cell"]',
)
FLAVOR_NAME_SEL, JOB_WORKUNIT_CELL_SEL = (
    '[data-testid="flavor-main-name"]',
    '[data-testid="job-workunit-cell"]',
)
JOBS_PANE_QUEUE_NAME_SEL = '[data-testid="target-queue-name"]'
SMART_FINDER_ROOT_SEL = "xmc-smart-resource-finder"
JOBS_CONTENT_SEL = '[data-testid="jobs-empty-state"], [data-testid="jobs-table"]'
JOBS_TABLE_ROW_SEL = 'xmc-queue-jobs-table [data-testid="job-row"]'
JOB_EXPERIMENT_LINK_SEL_TMPL = 'a[data-testid="job-link"][href="/experiments/{xid}"]'
JOBS_NEXT_PAGE_BTN_SEL = (
    "button.mat-mdc-paginator-navigation-next:not([disabled])"
    ':not([aria-disabled="true"]):not(.mat-mdc-button-disabled)'
)
PREDICTABILITY_EMPTY_SEL = '[data-testid="predictability-empty-state"]'
PREDICTABILITY_STATS_GRID_SEL = '[data-testid="predictability-stats-grid"]'
PREDICTABILITY_CONTENT_SEL = (
    '[data-testid="predictability-stats-grid"], [data-testid="predictability-spinner"]'
)
PREDICTABILITY_METRIC_VAL_SEL = '[data-testid="predictability-metric-value"]'
RESOURCE_SELECT_SEL = 'mat-select[data-testid="resource-type-select"]'
# ui-contract: queue_list.ng.html renders velocityPeriodOptions ('1h', '24h', '7d') as button text.
VELOCITY_PILL_SEL_TMPL = 'button[data-testid="velocity-pill"]:has-text("{tf}")'
# ui-contract: queue_list.ng.html renders velocityPeriodOptions ('1h', '24h', '7d') as button text.
ACTIVE_PILL_SEL_TMPL = 'button[data-testid="velocity-pill"][aria-pressed="true"]:has-text("{tf}")'
AVAILABILITY_TAB_SEL = 'button[data-testid="subnav-tab-resource-availability"]'
SMART_FINDER_TAB_SEL = 'button[data-testid="subnav-tab-smart-finder"]'

DASHBOARDS_LIST_ROOT_SEL = "xmc-dashboards-list-page"
DASHBOARDS_DETAIL_ROOT_SEL = "xmc-dashboards-page"
DASHBOARD_ROW_SEL = 'tr[data-testid="dashboard-row"]'
DASHBOARD_FILTER_INPUT_SEL = 'input[data-testid="dashboard-filter-input"]'
DASHBOARD_CLEAR_FILTER_BTN_SEL = 'button[aria-label="Clear search query"]'
DASHBOARD_EMPTY_STATE_SEL = '[data-testid="dashboard-empty-state"]'
DASHBOARD_LINK_SEL, DASHBOARD_TITLE_TEXT_SEL = (
    'a[data-testid="dashboard-link"]',
    '[data-testid="dashboard-title-text"]',
)
DASHBOARD_RESOURCE_NAME_SEL, DASHBOARD_BADGE_COUNT_SEL = (
    '[data-testid="dashboard-resource-name"]',
    '[data-testid="dashboard-badge-count"]',
)
DASHBOARD_DETAIL_TITLE_SEL = 'h1[data-testid="dashboard-detail-title"]'
CHART_CARD_SEL = "xmc-dashboard-grid xmc-chart-card"
CHART_SPINNER_SEL, CHART_ERROR_SEL = (
    '[data-testid="chart-loading-spinner"]',
    'xmc-chart-card [data-testid="chart-error-container"]',
)
CHART_HEADER_LINK_SEL = 'a[data-testid="chart-header-link"]'
SUMMARY_BOX_SEL, SUMMARY_VALUE_SEL = "xmc-summary-box", '[data-testid="summary-value"]'
PLOT_CANVAS_SEL = 'xmc-plot-card [data-testid="plot-canvas"] canvas'

ADMIN_PANEL_ROOT_SEL = "xmc-admin-panel"
ADMIN_PAGE_TITLE_SEL, ADMIN_TIMESTAMP_BADGE_SEL = (
    'h1[data-testid="admin-page-title"]',
    '[data-testid="admin-timestamp-badge"]',
)
ADMIN_FOOTER_SEL, SYSTEM_INFO_CARD_SEL = (
    'footer[data-testid="admin-panel-footer"]',
    '[data-testid="system-info-card"]',
)
APP_VERSION_VAL_SEL, ESS_SCHEMA_VAL_SEL = (
    '[data-testid="app-version-value"]',
    '[data-testid="ess-schema-value"]',
)
XID_SCHEMA_VAL_SEL = '[data-testid="xid-schema-value"]'
HEALTH_OVERALL_BADGE_SEL = '[data-testid="health-overall-badge"]'
HEALTH_REFRESH_BTN_SEL = 'button[data-testid="health-refresh-button"]'
HEALTH_REFRESH_READY_SEL = 'button[data-testid="health-refresh-button"]:not([disabled])'
HEALTH_LAST_CHECKED_SEL = '[data-testid="health-last-checked"]'
HEALTH_GROUP_TITLE_SEL = '[data-testid="health-group-title"]'
HEALTH_ITEM_ROW_SEL, HEALTH_ITEM_NAME_SEL = (
    '[data-testid="health-item-row"]',
    '[data-testid="health-item-name"]',
)
HEALTH_ITEM_STATUS_SEL = '[data-testid="health-item-status"]'
CONFIG_FILTER_INPUT_SEL = 'input[data-testid="config-filter-input"]'
CONFIG_GROUP_ROW_SEL = 'tr[data-testid="config-group-row"]'
CONFIG_GROUP_NAME_SEL, CONFIG_GROUP_ICON_SEL = (
    '[data-testid="config-group-name"]',
    '[data-testid="config-group-icon"]',
)
CONFIG_ROW_SEL, CONFIG_KEY_CELL_SEL = (
    'tr[data-testid="config-row"]',
    'td[data-testid="config-key-cell"]',
)
CONFIG_VAL_CELL_SEL, CONFIG_NO_RESULTS_SEL = (
    'td[data-testid="config-val-cell"]',
    'tr[data-testid="config-no-results-row"]',
)
