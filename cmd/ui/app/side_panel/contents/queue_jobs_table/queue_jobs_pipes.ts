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

import { DashboardJob, Resource } from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';
import { ResourceType } from 'xmanager_cloud/ui/app/common/components/resource_chip/resource_chip';
import { ResourceChipData } from 'xmanager_cloud/ui/app/common/components/resource_chip/utils/resource_set_to_resource_chips';
import { StatusOption } from 'xmanager_cloud/ui/app/common/components/status_chip/status_chip';

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

function formatMemoryBytes(bytes: number): string {
  if (!bytes || bytes <= 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB', 'EB'];
  const calculatedIndex = Math.floor(Math.log(bytes) / Math.log(k));
  const i = Math.max(0, Math.min(calculatedIndex, sizes.length - 1));
  const val = bytes / Math.pow(k, i);
  const formattedVal = Number.isInteger(val)
    ? val.toString()
    : parseFloat(val.toFixed(2)).toString();
  return `${formattedVal} ${sizes[i]}`;
}

function formatAcceleratorType(typeRaw: string, count: number): ResourceChipData {
  let displayType = typeRaw;
  if (displayType.includes('/')) {
    displayType = displayType.split('/').pop() ?? displayType;
  }
  const lower = displayType.toLowerCase();
  if (lower === 'gpu') {
    displayType = 'GPU';
  } else if (lower === 'tpu') {
    displayType = 'TPU';
  }

  const text = displayType ? `${count} ${displayType}` : `${count}`;
  return { resourceType: ResourceType.GPU, text };
}

function formatSingleResource(res: Resource): ResourceChipData {
  const typeRaw = (res.resourceType || '').trim();
  const typeLower = typeRaw.toLowerCase();
  const count = res.count ?? 0;

  if (typeLower === 'cpu') {
    const formattedCount = count.toLocaleString('en-US');
    const text = count === 1 ? `${formattedCount} core` : `${formattedCount} cores`;
    return { resourceType: ResourceType.CPU, text };
  }

  if (typeLower === 'memory') {
    return { resourceType: ResourceType.MEMORY, text: formatMemoryBytes(count) };
  }

  return formatAcceleratorType(typeRaw, count);
}

/**
 * Transforms a DashboardJob or array of Resource objects into an array of ResourceChipData.
 */
@Pipe({
  name: 'queueJobResources',
  standalone: true,
})
export class QueueJobResourcesPipe implements PipeTransform {
  transform(jobOrResources?: DashboardJob | Resource[] | null): ResourceChipData[] {
    if (!jobOrResources) return [];

    let resources: Resource[] | undefined;
    if (Array.isArray(jobOrResources)) {
      resources = jobOrResources;
    } else if (typeof jobOrResources === 'object' && 'requestedResources' in jobOrResources) {
      resources = jobOrResources.requestedResources;
    }

    if (!resources || !Array.isArray(resources) || resources.length === 0) {
      return [];
    }

    return resources
      .filter((res): res is Resource => Boolean(res))
      .map((res) => formatSingleResource(res));
  }
}
