// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
const EXPERIMENT_REGEX = /^experiments\/([^/]+)$/;
const WORK_UNIT_REGEX = /^experiments\/([^/]+)\/workUnits\/([^/]+)$/;
const ARTIFACT_REGEX = /^artifacts\/([^/]+)$/;

// Charts and dashboards are addressed under several parents, so these anchor on the collection
// segment rather than the whole name: "experiments/{xid}/workUnits/{wid}/charts/{chart_id}",
// "dashboards/{dashboard_id}/charts/{chart_id}" and the bare "charts/{chart_id}" all resolve.
const CHART_REGEX = /(?:^|\/)charts\/([^/]+)$/;
const DASHBOARD_REGEX = /^dashboards\/([^/]+)$/;

/**
 * Parses an experiment name and returns the experiment ID (xid).
 * Expected format: experiments/{xid}
 */
export function parseExperimentName(name: string): string | null {
  const match = name.match(EXPERIMENT_REGEX);
  return match ? match[1] : null;
}

/**
 * Parses a work unit name and returns the experiment ID (xid) and work unit ID
 * (wid).
 * Expected format: experiments/{xid}/workUnits/{wid}
 */
export function parseWorkUnitName(name: string): { xid: string; wid: string } | null {
  const match = name.match(WORK_UNIT_REGEX);
  return match ? { xid: match[1], wid: match[2] } : null;
}

/**
 * Parses an artifact name and returns the artifact ID (aid).
 * Expected format: artifacts/{aid}
 */
export function parseArtifactName(name: string): string | null {
  const match = name.match(ARTIFACT_REGEX);
  return match ? match[1] : null;
}

/*
 * The dashboard helpers below are deliberately lenient where the parsers above are strict. They
 * read IDs out of names the server returned *and* out of router parameters, which carry a bare ID
 * with no collection prefix, so a bare value passes through unchanged and an unparseable one
 * yields '' rather than null. The `extract` prefix marks that weaker contract.
 */

/**
 * Extracts the chart ID from a chart resource name, or '' if the name is not one.
 *
 * Matching on the "charts/" segment rather than taking the trailing path element means a name for
 * some other resource cannot be mistaken for a chart: "experiments/123/workUnits/1" yields '',
 * not the work unit ID.
 */
export function extractChartId(name?: string): string {
  if (!name) return '';
  const match = name.match(CHART_REGEX);
  if (match) return match[1];
  return name.includes('/') ? '' : name;
}

/**
 * Extracts the dashboard ID from a "dashboards/{dashboard_id}" resource name, or '' if the name is
 * not one. A bare ID passes through unchanged.
 */
export function extractDashboardId(name?: string): string {
  if (!name) return '';
  const match = name.match(DASHBOARD_REGEX);
  if (match) return match[1];
  return name.includes('/') ? '' : name;
}

/**
 * Builds the "experiments/{xid}/workUnits/{wid}" parent that chart creation is scoped to.
 *
 * wid defaults to 1 because a chart is always created against a concrete work unit, while the
 * view models carry it as optional; without the default an absent value reaches the server as the
 * literal parent "experiments/{xid}/workUnits/undefined".
 */
export function formatWorkUnitParent(xid?: number | string, wid?: number | string): string {
  return `experiments/${xid}/workUnits/${wid ?? 1}`;
}
