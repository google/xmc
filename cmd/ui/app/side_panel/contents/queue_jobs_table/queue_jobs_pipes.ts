// Copyright 2026 Google LLC
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

import { Pipe, PipeTransform } from '@angular/core';

import { ResourceType } from 'app/common/components/resource_chip/resource_chip';
import { ResourceChipData } from 'app/common/components/resource_chip/resource_set_to_resource_chips';
import { StatusOption } from 'app/common/components/status_chip/status_chip';
import { DashboardJob, Resource } from 'app/protos/rod';

/** Known job status string constants. */
export const JOB_STATUS_STRINGS = {
  RUNNING: 'RUNNING',
  STARTING: 'STARTING',
  SCHEDULING: 'SCHEDULING',
  INITIALIZING: 'INITIALIZING',
  SUCCEEDED: 'SUCCEEDED',
  COMPLETED: 'COMPLETED',
  FAILED: 'FAILED',
  CANCELLED: 'CANCELLED',
  STOPPED: 'STOPPED',
} as const;

export const NOT_APPLICABLE_RUNNING_DISPLAY = 'N/A - running';

/** Known resource type string constants. */
export const RESOURCE_TYPE_CPU = 'cpu';
export const RESOURCE_TYPE_MEMORY = 'memory';

/**
 * Transforms a DashboardJob into a typed StatusOption enum for xmc-status-chip rendering.
 */
@Pipe({
  name: 'queueJobStatus',
  standalone: true,
})
export class QueueJobStatusPipe implements PipeTransform {
  private static readonly STATUS_MAP = new Map<string, StatusOption>([
    [JOB_STATUS_STRINGS.RUNNING, StatusOption.RUNNING],
    [JOB_STATUS_STRINGS.STARTING, StatusOption.STARTING],
    [JOB_STATUS_STRINGS.SCHEDULING, StatusOption.SCHEDULING],
    [JOB_STATUS_STRINGS.INITIALIZING, StatusOption.INITIALIZING],
    [JOB_STATUS_STRINGS.SUCCEEDED, StatusOption.COMPLETED],
    [JOB_STATUS_STRINGS.COMPLETED, StatusOption.COMPLETED],
    [JOB_STATUS_STRINGS.FAILED, StatusOption.FAILED],
    [JOB_STATUS_STRINGS.CANCELLED, StatusOption.STOPPED],
    [JOB_STATUS_STRINGS.STOPPED, StatusOption.STOPPED],
  ]);

  transform(job?: DashboardJob | null): StatusOption {
    if (!job?.status) return StatusOption.PENDING;
    const s = String(job.status).toUpperCase();
    return QueueJobStatusPipe.STATUS_MAP.get(s) ?? StatusOption.PENDING;
  }
}

/**
 * Transforms a DashboardJob into its display position string ("N/A - running", numerical position, or "-").
 */
@Pipe({
  name: 'queueJobPosition',
  standalone: true,
})
export class QueueJobPositionPipe implements PipeTransform {
  transform(job?: DashboardJob | null): string {
    const status = String(job?.status || '').toUpperCase();
    if (
      status === JOB_STATUS_STRINGS.RUNNING ||
      status === JOB_STATUS_STRINGS.STARTING ||
      status === JOB_STATUS_STRINGS.INITIALIZING
    ) {
      return NOT_APPLICABLE_RUNNING_DISPLAY;
    }
    const pos = job?.positionInQueue;
    if (pos === undefined || pos === null || pos <= 0) {
      return '-';
    }
    return String(pos);
  }
}

export const RESOURCE_TYPE_GPU = 'gpu';
export const RESOURCE_TYPE_TPU = 'tpu';

const GPU_RESOURCE_KEYWORDS = [
  RESOURCE_TYPE_GPU,
  'nvidia',
  'h100',
  'a100',
  'v100',
  'l4',
  't4',
  'p100',
  'k80',
];

function isGpuResource(lower: string): boolean {
  return GPU_RESOURCE_KEYWORDS.some((kw) => lower.includes(kw));
}

function getResourceTypeEnum(typeRaw?: string): ResourceType {
  const lower = (typeRaw || '').trim().toLowerCase();
  if (lower === RESOURCE_TYPE_CPU) return ResourceType.CPU;
  if (lower === RESOURCE_TYPE_MEMORY) return ResourceType.MEMORY;
  if (lower.includes(RESOURCE_TYPE_TPU)) return ResourceType.TPU;
  if (isGpuResource(lower)) return ResourceType.GPU;

  if (typeRaw) {
    console.warn(`Unrecognized resource type: "${typeRaw}"`);
  }
  return ResourceType.GPU;
}

function formatSingleResource(res: Resource): ResourceChipData {
  const type = (res.resourceType || '').trim();
  const count = res.count ?? 0;
  const lower = type.toLowerCase();

  let text = `${count}`;
  if (type) {
    if (lower === RESOURCE_TYPE_CPU || lower === RESOURCE_TYPE_MEMORY) {
      text = `${type}: ${count}`;
    } else {
      text = `${count} x ${type}`;
    }
  }

  return {
    resourceType: getResourceTypeEnum(type),
    text,
  };
}

/**
 * Transforms an array of Resource objects into an array of ResourceChipData.
 */
@Pipe({
  name: 'queueJobResources',
  standalone: true,
})
export class QueueJobResourcesPipe implements PipeTransform {
  transform(resources?: Resource[] | null): ResourceChipData[] {
    if (!resources || !Array.isArray(resources) || resources.length === 0) {
      return [];
    }

    return resources
      .filter((res): res is Resource => Boolean(res))
      .map((res) => formatSingleResource(res));
  }
}
