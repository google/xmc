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
  ClusterQueueSecondaryMetrics,
  QueuePredictability,
  RangePreset,
} from 'xmanager_cloud/api/protos/rod/api.jsonpb_decls';

const rateFormatter = new Intl.NumberFormat('en-US', {
  maximumFractionDigits: 2,
  useGrouping: false,
});

/**
 * Format numerical velocity rate cleanly.
 */
export function formatRate(val: number): string {
  if (val === 0) return '0';
  return rateFormatter.format(val);
}

/**
 * Map period string to RangePreset enum.
 */
export function mapPeriodToRangePreset(period: string): RangePreset {
  switch (period) {
    case '1h':
    case '1 hour':
      return 'RANGE_PRESET_LAST_HOUR';
    case '24h':
    case '1 day':
      return 'RANGE_PRESET_LAST_DAY';
    case '7d':
    case '7 days':
      return 'RANGE_PRESET_LAST_WEEK';
    default:
      return 'RANGE_PRESET_LAST_WEEK';
  }
}

/**
 * Find QueuePredictability matching the given timeframe period and optional dominant resource type.
 */
export function findQueuePredictability(
  queue: ClusterQueueSecondaryMetrics,
  period: string,
  dominantResource?: string | null,
): QueuePredictability | undefined {
  const targetRange = mapPeriodToRangePreset(period);
  const predictabilities = queue?.queuePredictability ?? [];

  if (!dominantResource) {
    return predictabilities.find((p) => p.velocityRange === targetRange) ?? predictabilities[0];
  }

  const normDominant = dominantResource.trim().toLowerCase();
  return predictabilities.find(
    (p) =>
      p.velocityRange === targetRange &&
      (p.resourceVelocity?.resourceType?.toLowerCase() === normDominant ||
        p.pendingResourceRequests?.resourceType?.toLowerCase() === normDominant),
  );
}

/**
 * Calculate pending jobs count for a cluster queue.
 */
export function getPendingJobsCount(queue: ClusterQueueSecondaryMetrics): number {
  return queue?.queuePredictability?.find((p) => p.pendingJobs != null)?.pendingJobs ?? 0;
}

/**
 * Calculate resource velocity string for a cluster queue over a timeframe.
 */
export function calculateResourceVelocity(
  queue: ClusterQueueSecondaryMetrics,
  period: string,
  dominantResource?: string | null,
): string {
  const match = findQueuePredictability(queue, period, dominantResource);
  const vel = match?.resourceVelocity?.velocity ?? 0;
  const resType = match?.resourceVelocity?.resourceType ?? dominantResource ?? 'units';
  return `~${formatRate(vel)} ${resType}/hr`;
}

/**
 * Calculate job velocity string for a cluster queue over a timeframe.
 */
export function calculateJobVelocity(
  queue: ClusterQueueSecondaryMetrics,
  period: string,
  dominantResource?: string | null,
): string {
  const match = findQueuePredictability(queue, period, dominantResource);
  const vel = match?.jobVelocity ?? 0;
  return `${formatRate(vel)} jobs/hr`;
}

/**
 * Calculate pending resource requests display text for a cluster queue.
 */
export function calculatePendingRequestsDisplay(
  queue: ClusterQueueSecondaryMetrics,
  dominantResource?: string | null,
): string {
  const predictabilities = queue?.queuePredictability ?? [];
  const normDominant = dominantResource?.trim().toLowerCase();
  const match = normDominant
    ? predictabilities.find(
        (p) =>
          (p.resourceVelocity?.resourceType?.toLowerCase() === normDominant ||
            p.pendingResourceRequests?.resourceType?.toLowerCase() === normDominant) &&
          p.pendingResourceRequests?.count != null,
      )
    : predictabilities.find((p) => p.pendingResourceRequests?.count != null);
  return match?.pendingResourceRequests?.count != null
    ? formatRate(match.pendingResourceRequests.count)
    : '0';
}
