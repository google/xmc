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
import { Experiment } from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import { Url } from 'xmanager_cloud/experiment_state_server/proto/url.jsonpb_decls';
import { WorkUnit } from 'xmanager_cloud/experiment_state_server/proto/work_unit.jsonpb_decls';

import { parseExperimentName, parseWorkUnitName } from '../resource_name_extractor';

/**
 * Interface for Urls after processing.
 */
export interface ProcessedUrl extends Url {
  processedUrl: string | undefined;
  icon: string;
}

/**
 * Processes a list of URLs, replacing placeholders and sorting by priority.
 */
export function processUrls(urls: Url[], data: Experiment | WorkUnit): ProcessedUrl[] {
  return urls
    .map((url) => {
      const processedUrl = fillPlaceholders(url.url!, data);
      const icon = url.icon || 'link';
      return { ...url, processedUrl, icon };
    })
    .sort((a, b) => (a.priority ?? 0) - (b.priority ?? 0));
}

// Prefix used by the worker-side run retainer for the persisted copy of a
// completed JobSet. Under MultiKueue the live JobSet is deleted on completion,
// so the GKE console /overview link must target this retained "archive" copy
// (which mirrors the real terminal status) instead of the now-deleted original.
const ARCHIVE_PREFIX = 'xmc-archive-';

// Matches the GKE console JobSet overview link, whose target object is deleted
// under MultiKueue and must be redirected to the retained archive copy. Other
// URLs (e.g. Cloud Logs, which filter on the ORIGINAL jobset-name label the logs
// were emitted under) intentionally keep the original resource name.
const JOBSET_OVERVIEW_RE = /\/kubernetes\/aiml\/jobset\/.*\/overview/;

// Terminal work-unit states. The archive JobSet ("xmc-archive-<name>") is only
// created by the worker retainer AFTER the run finishes; before then only the
// ORIGINAL JobSet exists. So we must only redirect the overview link to the
// archive once the work unit is terminal, otherwise the link would 404 while the
// run is still executing.
const TERMINAL_STATES = new Set<string>([
  'COMPLETED',
  'FAILED',
  'PARTIALLY_FAILED',
  'STOPPED',
]);

function isTerminalWorkUnit(data: Experiment | WorkUnit): boolean {
  if (!('currentWorkUnitExecution' in data)) return false;
  const state = (data as WorkUnit).currentWorkUnitExecution?.status?.state;
  return state != null && TERMINAL_STATES.has(String(state));
}

function fillPlaceholders(urlString: string, data: Experiment | WorkUnit): string | undefined {
  const wu = data.name ? parseWorkUnitName(data.name) : null;
  const xid = wu ? wu.xid : data.name ? parseExperimentName(data.name) : null;
  const wid = wu ? wu.wid : undefined;

  const replacements: Record<string, string | null | undefined> = {
    XID: xid,
    WID: wid,
    CREATED: data.createTime,
  };

  const metadata =
    'currentWorkUnitExecution' in data
      ? (data as WorkUnit).currentWorkUnitExecution?.metadata
      : undefined;
  if (metadata) {
    replacements['RESOURCE_NAME'] = metadata.resourceName;
    replacements['NAMESPACE'] = metadata.namespace;
    replacements['SERVICE_ACCOUNT'] = metadata.serviceAccount;
    // Prefer the EXECUTING cluster (the worker that actually ran the JobSet, and
    // where the retained archive lives) and fall back to the management cluster.
    // For single-cluster runs these are identical.
    const cluster = metadata.executingCluster ?? metadata.managementCluster;
    if (cluster) {
      replacements['PROJECT'] = cluster.project;
      replacements['LOCATION'] = cluster.location;
      replacements['CLUSTER'] = cluster.clusterName;
    }
  }

  // Redirect the GKE Dashboard (JobSet /overview) link to the retained archive
  // copy ONLY once the work unit is terminal. While the run is still executing
  // the original JobSet exists (and the archive does not), so we must keep
  // pointing at the original to avoid a 404.
  const isJobsetOverview = JOBSET_OVERVIEW_RE.test(urlString);
  const useArchive =
    isJobsetOverview &&
    isTerminalWorkUnit(data) &&
    !!replacements['RESOURCE_NAME'] &&
    !replacements['RESOURCE_NAME']!.startsWith(ARCHIVE_PREFIX);
  const resourceForUrl = useArchive
    ? ARCHIVE_PREFIX + replacements['RESOURCE_NAME']
    : replacements['RESOURCE_NAME'];

  let missingValue = false;
  const processedUrl = urlString.replace(/\$\{(\w+)\}/g, (match, key) => {
    const value = key === 'RESOURCE_NAME' ? resourceForUrl : replacements[key];
    if (!value) missingValue = true;
    return value || match;
  });

  return missingValue ? undefined : processedUrl;
}
