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

import { UserResourceAllocation } from 'app/protos/rod';
import { matchesResourceFilter } from 'app/resource_dashboard/resource_filter';

/** Colors assigned to users in the stacked bar, in descending order of allocation. */
const USER_COLORS: readonly string[] = [
  '#1a73e8',
  '#e8710a',
  '#188038',
  '#d93025',
  '#9334e6',
  '#12b5cb',
  '#f9ab00',
  '#5f6368',
];

const UNKNOWN_USER = 'unknown';

/** One user's share of the dominant resource in the selected queue. */
export interface UserAllocationSegment {
  readonly user: string;
  readonly color: string;
  readonly runningPercent: number;
  readonly pendingPercent: number;
  readonly label: string;
}

/** Per-user breakdown of the dominant resource across all active jobs in a queue. */
export interface UserAllocationSummary {
  readonly unit: string;
  readonly totalsLabel: string;
  readonly segments: readonly UserAllocationSegment[];
}

interface UserTotals {
  readonly user: string;
  readonly running: number;
  readonly pending: number;
}

/** Returns the display unit for a resource type, e.g. "GPUs" for "nvidia.com/gpu: nvidia-l4". */
export function resolveResourceUnitLabel(resourceType: string): string {
  const lower = resourceType.trim().toLowerCase();
  if (lower === 'memory') return 'GiB';
  if (lower === 'cpu') return 'cores';
  if (lower.includes('tpu')) return 'chips';
  if (lower.includes('gpu')) return 'GPUs';
  return 'units';
}

/** Formats units with at most two decimals and no trailing zeros. */
export function formatAllocationUnits(value: number): string {
  return String(Math.round(value * 100) / 100);
}

function aggregateByUser(
  allocations: readonly UserResourceAllocation[],
  dominantResource: string,
): UserTotals[] {
  const totals = new Map<string, { running: number; pending: number }>();
  for (const alloc of allocations) {
    if (!matchesResourceFilter(alloc.resourceType ?? '', dominantResource)) continue;
    const user = alloc.user?.trim() || UNKNOWN_USER;
    const entry = totals.get(user) ?? { running: 0, pending: 0 };
    totals.set(user, {
      running: entry.running + (alloc.runningUnits ?? 0),
      pending: entry.pending + (alloc.pendingUnits ?? 0),
    });
  }
  return [...totals.entries()]
    .map(([user, t]) => ({ user, ...t }))
    .filter((t) => t.running + t.pending > 0)
    .sort(
      (a, b) => b.running + b.pending - (a.running + a.pending) || a.user.localeCompare(b.user),
    );
}

function formatUnitsLabel(running: number, pending: number, unit: string): string {
  const runningText = `${formatAllocationUnits(running)} running`;
  if (pending <= 0) return `${runningText} ${unit}`;
  return `${runningText} + ${formatAllocationUnits(pending)} pending ${unit}`;
}

/**
 * Summarizes the per-user allocation of the dominant resource. A generic parent such as
 * "nvidia.com/gpu" sums every matching model. Returns null when nothing matches.
 */
export function summarizeUserAllocations(
  allocations: readonly UserResourceAllocation[],
  dominantResource: string | null,
): UserAllocationSummary | null {
  if (!dominantResource) return null;
  const users = aggregateByUser(allocations, dominantResource);
  const totalRunning = users.reduce((sum, u) => sum + u.running, 0);
  const totalPending = users.reduce((sum, u) => sum + u.pending, 0);
  const grandTotal = totalRunning + totalPending;
  if (grandTotal <= 0) return null;
  const unit = resolveResourceUnitLabel(dominantResource);
  return {
    unit,
    totalsLabel: formatUnitsLabel(totalRunning, totalPending, unit),
    segments: users.map((u, i) => ({
      user: u.user,
      color: USER_COLORS[i % USER_COLORS.length],
      runningPercent: (u.running / grandTotal) * 100,
      pendingPercent: (u.pending / grandTotal) * 100,
      label: formatUnitsLabel(u.running, u.pending, unit),
    })),
  };
}
