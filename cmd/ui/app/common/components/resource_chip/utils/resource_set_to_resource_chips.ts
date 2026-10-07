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
import { ResourceSet } from 'xmanager_cloud/experiment_state_server/proto/resources.jsonpb_decls';

import { ResourceType } from '../resource_chip';

/** The data to display a resource chip */
export interface ResourceChipData {
  readonly resourceType: ResourceType;
  readonly text: string;
}

const CORE_COUNT_FORMATTER = new Intl.NumberFormat('en-US', {
  maximumFractionDigits: 1,
});

const BYTE_UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB', 'ZB', 'YB'] as const;

function formatCoreCount(cores: number): string {
  const formatted = CORE_COUNT_FORMATTER.format(cores);
  return cores === 1 ? `${formatted} core` : `${formatted} cores`;
}

function formatBytes(bytes: number, decimals = 2): string {
  if (isNaN(bytes) || bytes <= 0) {
    return '0 B';
  }
  const k = 1024;
  const dm = Math.max(0, decimals);
  const unitIndex = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), BYTE_UNITS.length - 1);
  return `${parseFloat((bytes / Math.pow(k, unitIndex)).toFixed(dm))} ${BYTE_UNITS[unitIndex]}`;
}

/** Converts a list of ResourceSet to a list of ResourceChipData. */
export function resourceSetsToResourceChipsData(resources: ResourceSet[]): ResourceChipData[] {
  return resources.flatMap((res) => {
    const chips: ResourceChipData[] = [];
    if (res.cpuResources) {
      const cores = Number(res.cpuResources.cpuMillicores ?? 0) / 1000;
      chips.push(
        {
          resourceType: ResourceType.CPU,
          text: formatCoreCount(cores),
        },
        {
          resourceType: ResourceType.MEMORY,
          text: formatBytes(Number(res.cpuResources.memoryBytes), 2),
        },
      );
    }
    if (res.gpuResources) {
      chips.push({
        resourceType: ResourceType.GPU,
        text: `${res.gpuResources.count} x ${res.gpuResources.type}`,
      });
    }
    if (res.tpuResources) {
      chips.push({
        resourceType: ResourceType.GPU,
        text: `${res.tpuResources.topology} x ${res.tpuResources.type}`,
      });
    }
    return chips;
  });
}
