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

import { QueueOverview, QueueResourceCapacity } from 'app/protos/rod';

/**
 * Pure Angular pipe to compute free/total capacity display text for a queue resource capacity.
 */
@Pipe({
  name: 'freeTotalDisplay',
  standalone: true,
  pure: true,
})
export class FreeTotalDisplayPipe implements PipeTransform {
  transform(capacity?: QueueResourceCapacity | null): string {
    if (!capacity) return '-';
    const total = capacity.totalCapacity ?? 0;
    const free = capacity.freeCapacity ?? 0;
    return `${free} / ${total}`;
  }
}

export const COHORT_STATE_BALANCED = 'BALANCED' as const;
export const COHORT_STATE_SQUEEZED = 'SQUEEZED' as const;
export const COHORT_STATE_BORROWING = 'BORROWING' as const;

export type CohortState =
  | typeof COHORT_STATE_BALANCED
  | typeof COHORT_STATE_SQUEEZED
  | typeof COHORT_STATE_BORROWING;

export interface CohortStateInfo {
  state: CohortState;
  label: string;
  badgeClass: string;
  icon: string;
  delta: number;
  tooltip?: string;
}

const EPSILON = 1e-5;

const BALANCED_STATE: CohortStateInfo = {
  state: COHORT_STATE_BALANCED,
  label: 'Balanced',
  badgeClass: 'balanced',
  icon: 'check_circle',
  delta: 0,
};

function buildSqueezedStateInfo(
  schedulable: number,
  freeNominal: number,
  delta: number,
): CohortStateInfo {
  return {
    state: COHORT_STATE_SQUEEZED,
    label: 'Squeezed by cohort',
    badgeClass: 'squeezed',
    icon: 'warning',
    delta: Math.abs(delta),
    tooltip: `Shared cohort pool depleted: Immediate headroom (${schedulable}) is lower than nominal free quota (${freeNominal}) due to cohort-wide workload demand.`,
  };
}

function buildBorrowingStateInfo(delta: number): CohortStateInfo {
  return {
    state: COHORT_STATE_BORROWING,
    label: 'Borrowing available',
    badgeClass: 'borrowing',
    icon: 'bolt',
    delta,
    tooltip: `Can borrow up to +${delta} beyond nominal quota from cohort.`,
  };
}

/**
 * Derives cohort sharing state info (BALANCED, SQUEEZED, BORROWING) from a QueueResourceCapacity.
 */
export function getCohortStateInfo(capacity?: QueueResourceCapacity | null): CohortStateInfo {
  if (!capacity) return BALANCED_STATE;

  const freeNominal = capacity.freeCapacity ?? 0;
  const schedulable = capacity.freeCapacityWithBorrowing ?? freeNominal;
  const delta = schedulable - freeNominal;

  if (delta < -EPSILON) {
    return buildSqueezedStateInfo(schedulable, freeNominal, delta);
  }

  if (delta > EPSILON) {
    return buildBorrowingStateInfo(delta);
  }

  return BALANCED_STATE;
}

/**
 * Pure Angular pipe to compute max schedulable display text for a queue resource capacity.
 */
@Pipe({
  name: 'maxSchedulableDisplay',
  standalone: true,
  pure: true,
})
export class MaxSchedulableDisplayPipe implements PipeTransform {
  transform(capacity?: QueueResourceCapacity | null): string {
    if (!capacity) return '-';
    const schedulable = capacity.freeCapacityWithBorrowing ?? capacity.freeCapacity ?? 0;
    return `${schedulable}`;
  }
}

/**
 * Pure Angular pipe to compute cohort state info for capacity headers and contextual banners.
 */
@Pipe({
  name: 'cohortStateInfo',
  standalone: true,
  pure: true,
})
export class CohortStateInfoPipe implements PipeTransform {
  transform(capacity?: QueueResourceCapacity | null): CohortStateInfo {
    return getCohortStateInfo(capacity);
  }
}

export interface PredictabilityMessageInfo {
  text: string;
  isHealthy: boolean;
}

/**
 * Pure Angular pipe to compute predictability status message and health status for a cluster queue.
 */
@Pipe({
  name: 'predictabilityMessage',
  standalone: true,
  pure: true,
})
export class PredictabilityMessagePipe implements PipeTransform {
  transform(queue?: QueueOverview | null): PredictabilityMessageInfo | null {
    if (!queue) return null;
    return { text: 'System is healthy. Job will start immediately.', isHealthy: true };
  }
}

export interface QueueStatusBadgeInfo {
  label: string;
  isHealthy: boolean;
}

/**
 * Pure Angular pipe to compute queue status badge info (label and health status).
 */
@Pipe({
  name: 'queueStatusBadge',
  standalone: true,
  pure: true,
})
export class QueueStatusBadgePipe implements PipeTransform {
  transform(queue?: QueueOverview | null): QueueStatusBadgeInfo | null {
    if (!queue) return null;
    return { label: 'Resources available', isHealthy: true };
  }
}
