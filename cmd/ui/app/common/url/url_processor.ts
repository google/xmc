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
import { Experiment, Url, WorkUnit } from 'app/protos/experiment_state_server';

import { parseExperimentName, parseWorkUnitName } from '../resource_name_extractor';

const EXECUTING_CLUSTER_KEYS = new Set(['PROJECT', 'LOCATION', 'CLUSTER']);
const NO_EXECUTING_CLUSTER_REASON = 'Unavailable: no executing cluster assigned';
const MISSING_METADATA_REASON = 'Unavailable: required metadata is missing';

/**
 * Interface for Urls after processing.
 */
export interface ProcessedUrl extends Url {
  processedUrl: string | undefined;
  icon: string;
  disableReason?: string;
}

interface PlaceholderResult {
  processedUrl: string | undefined;
  disableReason?: string;
}

/**
 * Processes a list of URLs, replacing placeholders and sorting by priority.
 */
export function processUrls(urls: Url[], data: Experiment | WorkUnit): ProcessedUrl[] {
  const replacements = buildReplacements(data);
  return urls
    .map((url) => {
      const { processedUrl, disableReason } = url.url
        ? fillPlaceholders(url.url, replacements)
        : { processedUrl: undefined, disableReason: MISSING_METADATA_REASON };
      const icon = url.icon || 'link';
      return {
        ...url,
        processedUrl,
        icon,
        ...(disableReason ? { disableReason } : {}),
      };
    })
    .sort((a, b) => (a.priority ?? 0) - (b.priority ?? 0));
}

function buildReplacements(data: Experiment | WorkUnit): Record<string, string | null | undefined> {
  const wu = data.name ? parseWorkUnitName(data.name) : null;
  const xid = wu ? wu.xid : data.name ? parseExperimentName(data.name) : null;
  const replacements: Record<string, string | null | undefined> = {
    XID: xid,
    WID: wu ? wu.wid : undefined,
    CREATED: data.createTime,
  };

  if ('currentWorkUnitExecution' in data && data.currentWorkUnitExecution?.metadata) {
    applyExecutionMetadata(replacements, data.currentWorkUnitExecution.metadata);
  }
  return replacements;
}

function applyExecutionMetadata(
  replacements: Record<string, string | null | undefined>,
  metadata: NonNullable<NonNullable<WorkUnit['currentWorkUnitExecution']>['metadata']>,
): void {
  replacements['RESOURCE_NAME'] = metadata.resourceName;
  replacements['NAMESPACE'] = metadata.namespace;
  replacements['SERVICE_ACCOUNT'] = metadata.serviceAccount;
  replacements['MGMT_PROJECT'] = metadata.managementCluster?.project;
  replacements['PROJECT'] = metadata.executingCluster?.project;
  replacements['LOCATION'] = metadata.executingCluster?.location;
  // When xmc.google.com/cluster-name is omitted on the MultiKueueCluster,
  // multiKueueClusterName serves as a best-effort stand-in for the GKE cluster name.
  replacements['CLUSTER'] =
    metadata.executingCluster?.clusterName || metadata.multiKueueClusterName;
}

function fillPlaceholders(
  urlString: string,
  replacements: Record<string, string | null | undefined>,
): PlaceholderResult {
  const missingKeys = new Set<string>();
  const processedUrl = urlString.replace(/\$\{(\w+)\}/g, (match, key: string) => {
    const value = replacements[key];
    if (!value) {
      missingKeys.add(key);
    }
    return value || match;
  });

  if (missingKeys.size === 0) {
    return { processedUrl };
  }
  const missingExecutingCluster = [...missingKeys].some((key) => EXECUTING_CLUSTER_KEYS.has(key));
  return {
    processedUrl: undefined,
    disableReason: missingExecutingCluster ? NO_EXECUTING_CLUSTER_REASON : MISSING_METADATA_REASON,
  };
}
