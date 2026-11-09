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
QUEUE_CARD_SEL, RESOURCE_OPTION_SEL = "xmc-rod-queue-card .queue-card", "mat-option"
QUEUE_TITLE_SEL, JOB_POSITION_CELL_SEL = "xmc-rod-queue-card .queue-title", ".position-cell"
FLAVOR_NAME_SEL, JOB_WORKUNIT_CELL_SEL = ".capacity-box .flavor-main-name", ".workunit-cell"
JOBS_PANE_QUEUE_NAME_SEL = "xmc-queue-jobs-table .target-queue-name"
SMART_FINDER_ROOT_SEL = "xmc-smart-resource-finder"
JOBS_CONTENT_SEL = "xmc-queue-jobs-table .jobs-mat-table, xmc-queue-jobs-table .jobs-empty-state"
JOBS_TABLE_ROW_SEL = "xmc-queue-jobs-table table.jobs-mat-table tr.mat-mdc-row"
JOB_EXPERIMENT_LINK_SEL_TMPL = 'a.job-link[href="/experiments/{xid}"]'
JOBS_NEXT_PAGE_BTN_SEL = (
    "button.mat-mdc-paginator-navigation-next:not([disabled])"
    ':not([aria-disabled="true"]):not(.mat-mdc-button-disabled)'
)
PREDICTABILITY_EMPTY_SEL = ".predictability-empty-state"
PREDICTABILITY_STATS_GRID_SEL = ".predictability-stats-grid"
PREDICTABILITY_CONTENT_SEL = ".predictability-stats-grid, .predictability-spinner-container"
PREDICTABILITY_METRIC_VAL_SEL = ".predictability-stats-grid .cell-value"
RESOURCE_SELECT_SEL = "xmc-smart-resource-finder mat-select.query-select-box"
# ui-contract: queue_list.ng.html renders each velocityPeriods value as the pill text.
VELOCITY_PILL_SEL_TMPL = 'xmc-rod-queue-list button.toggle-pill:has-text("{tf}")'
# ui-contract: queue_list.ng.html renders each velocityPeriods value as the pill text.
ACTIVE_PILL_SEL_TMPL = 'xmc-rod-queue-list button.toggle-pill.active-toggle:has-text("{tf}")'
# ui-contract: subnav_tabs.ng.html tab buttons share one class; label is the only key.
AVAILABILITY_TAB_SEL = 'xmc-rod-subnav-tabs button.subnav-tab:has-text("Resource availability")'
# ui-contract: subnav_tabs.ng.html tab buttons share one class; label is the only key.
SMART_FINDER_TAB_SEL = 'xmc-rod-subnav-tabs button.subnav-tab:has-text("Smart resource finder")'

DASHBOARDS_LIST_ROOT_SEL = "xmc-dashboards-list-page"
DASHBOARDS_DETAIL_ROOT_SEL = "xmc-dashboards-page"
DASHBOARD_ROW_SEL = "table.dashboards-table tr.dashboard-row"
DASHBOARD_FILTER_INPUT_SEL = '.filter-toolbar input[placeholder*="Filter dashboards"]'
DASHBOARD_CLEAR_FILTER_BTN_SEL = 'button[aria-label="Clear search query"]'
DASHBOARD_EMPTY_STATE_SEL = ".empty-state-container"
DASHBOARD_LINK_SEL, DASHBOARD_TITLE_TEXT_SEL = "a.dashboard-link", ".dashboard-title-text"
DASHBOARD_RESOURCE_NAME_SEL, DASHBOARD_BADGE_COUNT_SEL = ".dashboard-resource-name", ".badge-count"
DASHBOARD_DETAIL_TITLE_SEL = "h1.dashboard-title"
CHART_CARD_SEL = "xmc-dashboard-grid xmc-chart-card"
CHART_SPINNER_SEL, CHART_ERROR_SEL = ".loading-container", "xmc-chart-card .error-container"
CHART_HEADER_LINK_SEL = "a.chart-header-link"
SUMMARY_BOX_SEL, SUMMARY_VALUE_SEL = "xmc-summary-box", ".summary-value"
PLOT_CANVAS_SEL = "xmc-plot-card div.plot-canvas canvas"

ADMIN_PANEL_ROOT_SEL = "xmc-admin-panel"
ADMIN_PAGE_TITLE_SEL, ADMIN_TIMESTAMP_BADGE_SEL = "h1.page-title", ".timestamp-badge"
ADMIN_FOOTER_SEL, SYSTEM_INFO_CARD_SEL = (
    "footer.admin-panel-footer",
    "xmc-system-info-card .system-info-card",
)
APP_VERSION_VAL_SEL, ESS_SCHEMA_VAL_SEL = ".app-version-value", ".ess-schema-value"
XID_SCHEMA_VAL_SEL = ".xid-schema-value"
HEALTH_OVERALL_BADGE_SEL = "xmc-health-status-section .card-header .status-badge"
HEALTH_REFRESH_BTN_SEL = "xmc-health-status-section button.refresh-button"
HEALTH_REFRESH_READY_SEL = "xmc-health-status-section button.refresh-button:not([disabled])"
HEALTH_LAST_CHECKED_SEL = "xmc-health-status-section .last-checked"
HEALTH_GROUP_TITLE_SEL = "xmc-health-status-group-card .group-title"
HEALTH_ITEM_ROW_SEL, HEALTH_ITEM_NAME_SEL = ".group-items-list .item-row", ".item-name"
HEALTH_ITEM_STATUS_SEL = ".item-status-text"
CONFIG_FILTER_INPUT_SEL = "xmc-config-entries-card input.filter-input"
CONFIG_GROUP_ROW_SEL = "xmc-config-entries-card tr.group-header-row"
CONFIG_GROUP_NAME_SEL, CONFIG_GROUP_ICON_SEL = ".group-display-name", ".group-toggle-icon"
CONFIG_ROW_SEL, CONFIG_KEY_CELL_SEL = "xmc-config-entries-card tr.config-row", "td.cell-key"
CONFIG_VAL_CELL_SEL, CONFIG_NO_RESULTS_SEL = "td.cell-value", "tr.no-results-row"
