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

import { DashboardJob } from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';
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
  transform(job?: DashboardJob | null): StatusOption {
    if (!job?.status) return StatusOption.PENDING;
    const s = String(job.status).toUpperCase();
    switch (s) {
      case JOB_STATUS_STRINGS.RUNNING:
        return StatusOption.RUNNING;
      case JOB_STATUS_STRINGS.STARTING:
        return StatusOption.STARTING;
      case JOB_STATUS_STRINGS.SCHEDULING:
        return StatusOption.SCHEDULING;
      case JOB_STATUS_STRINGS.INITIALIZING:
        return StatusOption.INITIALIZING;
      case JOB_STATUS_STRINGS.SUCCEEDED:
      case JOB_STATUS_STRINGS.COMPLETED:
        return StatusOption.COMPLETED;
      case JOB_STATUS_STRINGS.FAILED:
        return StatusOption.FAILED;
      case JOB_STATUS_STRINGS.CANCELLED:
      case JOB_STATUS_STRINGS.STOPPED:
        return StatusOption.STOPPED;
      default:
        return StatusOption.PENDING;
    }
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
    const num = Number(pos);
    if (
      pos === undefined ||
      pos === null ||
      pos === '' ||
      isNaN(num) ||
      num < 0
    ) {
      return '-';
    }
    return String(num);
  }
}
