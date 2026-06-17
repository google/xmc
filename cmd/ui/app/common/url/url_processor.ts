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

function fillPlaceholders(urlString: string, data: Experiment | WorkUnit): string | undefined {
  const wu = data.name ? parseWorkUnitName(data.name) : null;
  const xid = wu ? wu.xid : (data.name ? parseExperimentName(data.name) : null);
  const wid = wu ? wu.wid : undefined;

  const replacements: Record<string, string | null | undefined> = {
    XID: xid,
    WID: wid,
    CREATED: data.createTime,
  };

  const metadata = ('currentWorkUnitExecution' in data)
    ? (data as WorkUnit).currentWorkUnitExecution?.metadata
    : undefined;
  if (metadata) {
    replacements['RESOURCE_NAME'] = metadata.resourceName;
    replacements['NAMESPACE'] = metadata.namespace;
    replacements['SERVICE_ACCOUNT'] = metadata.serviceAccount;
    if (metadata.managementCluster) {
      replacements['PROJECT'] = metadata.managementCluster.project;
      replacements['LOCATION'] = metadata.managementCluster.location;
      replacements['CLUSTER'] = metadata.managementCluster.clusterName;
    }
  }

  let missingValue = false;
  const processedUrl = urlString.replace(/\$\{(\w+)\}/g, (match, key) => {
    const value = replacements[key];
    if (!value) missingValue = true;
    return value || match;
  });

  return missingValue ? undefined : processedUrl;
}
