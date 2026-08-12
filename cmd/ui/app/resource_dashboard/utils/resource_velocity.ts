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

import {
  QueuePredictability,
  QueueSecondaryData,
  RangePreset,
} from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';

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
  switch (period) {
    case '1h':
    case '1 hour':
      return 1;
    case '24h':
    case '1 day':
      return 24;
    case '7d':
    case '7 days':
      return 168;
    default:
      return 168;
  }
}

/**
 * Map period string to RangePreset enum.
 */
export function mapPeriodToRangePreset(period: string): RangePreset {
  switch (period) {
    case '1h':
    case '1 hour':
      return 'LAST_HOUR';
    case '24h':
    case '1 day':
      return 'LAST_DAY';
    case '7d':
    case '7 days':
      return 'LAST_WEEK';
    default:
      return 'LAST_WEEK';
  }
}

/**
 * Find QueuePredictability matching the given timeframe period.
 */
export function findQueuePredictability(
  queue: QueueSecondaryData,
  period: string,
): QueuePredictability | undefined {
  const targetRange = mapPeriodToRangePreset(period);
  const predictabilities = queue?.queuePredictability ?? [];
  return (
    predictabilities.find((p) => p.velocityRange === targetRange) ?? predictabilities[0]
  );
}

/**
 * Calculate pending jobs count for a cluster queue.
 */
export function getPendingJobsCount(queue: QueueSecondaryData): number {
  const predictabilities = queue?.queuePredictability ?? [];
  for (const p of predictabilities) {
    if (p.pendingJobs !== undefined && p.pendingJobs !== null) {
      return p.pendingJobs;
    }
  }
  return 0;
}

/**
 * Calculate resource velocity string for a cluster queue over a timeframe.
 */
export function calculateResourceVelocity(queue: QueueSecondaryData, period: string): string {
  const match = findQueuePredictability(queue, period);
  const vel = match?.resourceVelocity?.velocity ?? 0;
  const resType = match?.resourceVelocity?.resourceType ?? 'units';
  return `~${formatRate(vel)} ${resType}/hr`;
}

/**
 * Calculate job velocity string for a cluster queue over a timeframe.
 */
export function calculateJobVelocity(queue: QueueSecondaryData, period: string): string {
  const match = findQueuePredictability(queue, period);
  const vel = match?.jobVelocity ?? 0;
  return `${formatRate(vel)} jobs/hr`;
}

/**
 * Calculate pending resource requests display text for a cluster queue.
 */
export function calculatePendingRequestsDisplay(queue: QueueSecondaryData): string {
  const predictabilities = queue?.queuePredictability ?? [];
  for (const p of predictabilities) {
    if (
      p.pendingResourceRequests?.count !== undefined &&
      p.pendingResourceRequests?.count !== null
    ) {
      return formatRate(p.pendingResourceRequests.count);
    }
  }
  return '0';
}
