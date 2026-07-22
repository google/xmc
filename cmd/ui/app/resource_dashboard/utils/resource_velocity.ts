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

import { ClusterQueue } from 'xmanager_cloud/ui/app/service/resource_types';

export const DEFAULT_UNITS_PER_PENDING_JOB = 8;

/**
 * Velocity period options for queue resource calculations.
 */
export interface VelocityPeriodOption {
  readonly value: string;
  readonly label: string;
}

export const VELOCITY_PERIOD_OPTIONS: readonly VelocityPeriodOption[] = [
  { value: '1h', label: '1 hour' },
  { value: '24h', label: '1 day' },
  { value: '7d', label: '7 days' },
];

/**
 * Format numerical velocity rate cleanly.
 */
export function formatRate(val: number): string {
  if (val === 0) return '0';
  if (Number.isInteger(val)) return `${val}`;
  return parseFloat(val.toFixed(2)).toString();
}

/**
 * Convert velocity period string to numeric hours.
 */
export function getVelocityHours(period: string): number {
  if (period === '1h' || period === '1 hour') return 1;
  if (period === '24h' || period === '1 day') return 24;
  return 168;
}

/**
 * Calculate pending jobs count for a cluster queue.
 */
export function getPendingJobsCount(queue: Partial<ClusterQueue>): number {
  return queue.pendingJobsCount ?? queue.pendingWorkloads ?? 0;
}

/**
 * Calculate resource velocity string for a cluster queue over a timeframe.
 */
export function calculateResourceVelocity(queue: Partial<ClusterQueue>, period: string): string {
  if (queue.resourceVelocity) {
    return queue.resourceVelocity;
  }
  const dom = queue.dominantResource || 'CPU';
  const isGpu = dom.toLowerCase().includes('gpu') || dom.toLowerCase().includes('nvidia');
  const unitName = isGpu ? 'GPUs' : 'CPUs';

  const pending = getPendingJobsCount(queue);
  if (pending > 0) {
    return `~0 ${unitName}/hr`;
  }

  const hours = getVelocityHours(period);
  const totalUnits = queue.dominantResourceUnits ?? queue.admittedWorkloads ?? 0;
  const rate = totalUnits / hours;
  return `~${formatRate(rate)} ${unitName}/hr`;
}

/**
 * Calculate job velocity string for a cluster queue over a timeframe.
 */
export function calculateJobVelocity(queue: Partial<ClusterQueue>, period: string): string {
  if (queue.jobVelocity) {
    return queue.jobVelocity;
  }
  const pending = getPendingJobsCount(queue);
  if (pending > 0) {
    return '0 jobs/hr';
  }

  const hours = getVelocityHours(period);
  const totalJobs = queue.admittedJobsCount ?? queue.admittedWorkloads ?? 0;
  const rate = totalJobs / hours;
  return `${formatRate(rate)} jobs/hr`;
}

/**
 * Calculate pending resource requests display text for a cluster queue.
 */
export function calculatePendingRequestsDisplay(queue: Partial<ClusterQueue>): string {
  if (queue.requestedResources && queue.requestedResources !== '0') {
    return queue.requestedResources;
  }
  const pending = getPendingJobsCount(queue);
  if (pending === 0) return '0';

  const firstFlavor = queue.flavors?.[0];
  const rName = firstFlavor?.resourceName || firstFlavor?.name || 'CPU';
  const isGpu = rName.toLowerCase().includes('gpu');
  const unit = isGpu ? 'GPUs' : 'CPUs';
  return `${pending * DEFAULT_UNITS_PER_PENDING_JOB} ${unit}`;
}
