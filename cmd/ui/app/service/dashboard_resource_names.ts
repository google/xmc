// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

/**
 * @fileoverview Parsers for Dashboard Service resource names (AIP-122).
 *
 * The service assigns every resource name itself, so the client never constructs one. It only reads
 * the trailing collection ID back out of a name the server returned, in order to reference that
 * resource elsewhere (a router link, or a `chart_ids` entry on a dashboard).
 */

/**
 * Extracts the dashboard ID from a "dashboards/{dashboard_id}" resource name.
 *
 * A bare ID is returned unchanged so that router parameters, which carry the ID without its
 * collection prefix, can be passed straight through.
 */
export function extractDashboardId(name?: string): string {
  if (!name) return '';
  return name.startsWith('dashboards/') ? name.replace('dashboards/', '') : name;
}

/**
 * Extracts the chart ID from a chart resource name.
 *
 * Charts are addressed under several parents — "experiments/{xid}/workUnits/{wid}/charts/{chart_id}",
 * "dashboards/{dashboard_id}/charts/{chart_id}", and the bare "charts/{chart_id}" — and the chart ID
 * is the trailing segment in every one of those patterns.
 */
export function extractChartId(name?: string): string {
  if (!name) return '';
  return name.split('/').pop() ?? '';
}
