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

DASHBOARD_ROOT_SEL = "xmc-resource-dashboard"
QUEUE_CARD_SEL = "xmc-rod-queue-card .queue-card"
QUEUE_TITLE_SEL = "xmc-rod-queue-card .queue-title"
FLAVOR_NAME_SEL = ".capacity-box .flavor-main-name"
STATUS_BADGE_SEL = ".status-badge"
COPY_FLAG_BTN_SEL = "button.copy-flag-btn"
COPIED_FLAG_BTN_SEL = 'xmc-rod-queue-card button.copy-flag-btn:has-text("Copied")'
JOBS_PANE_QUEUE_NAME_SEL = "xmc-queue-jobs-table .target-queue-name"
JOBS_CONTENT_SEL = "xmc-queue-jobs-table .jobs-mat-table, xmc-queue-jobs-table .jobs-empty-state"
PREDICTABILITY_EMPTY_SEL = ".predictability-empty-state"
PREDICTABILITY_CONTENT_SEL = ".predictability-stats-grid, .predictability-spinner-container"
RESOURCE_SELECT_SEL = "xmc-smart-resource-finder mat-select.query-select-box"
RESOURCE_OPTION_SEL = "mat-option"
RESOURCE_AMOUNT_INPUT_SEL = "xmc-smart-resource-finder input.query-amount-input"
ADD_RESOURCE_BTN_SEL = "xmc-smart-resource-finder button.add-resource-btn"
REMOVE_RESOURCE_BTN_SEL = "xmc-smart-resource-finder button.remove-resource-btn"
RESOURCE_QUERY_ROW_SEL = "xmc-smart-resource-finder .query-row"
QUEUE_LIST_EMPTY_SEL = "xmc-rod-queue-list .empty-state"
VELOCITY_PILL_SEL_TMPL = 'xmc-rod-queue-list button.toggle-pill:has-text("{tf}")'
ACTIVE_PILL_SEL_TMPL = 'xmc-rod-queue-list button.toggle-pill.active-toggle:has-text("{tf}")'
AVAILABILITY_TAB_SEL = 'xmc-rod-subnav-tabs button.subnav-tab:has-text("Resource availability")'
SMART_FINDER_TAB_SEL = 'xmc-rod-subnav-tabs button.subnav-tab:has-text("Smart resource finder")'
WIP_BANNER_SEL = "xmc-wip-banner"
SMART_FINDER_ROOT_SEL = "xmc-smart-resource-finder"
