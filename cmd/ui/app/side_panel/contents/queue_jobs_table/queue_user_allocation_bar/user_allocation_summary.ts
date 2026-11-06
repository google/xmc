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

import { ResourceType } from 'app/common/components/resource_chip/resource_chip';
import { UserResourceAllocation } from 'app/protos/rod';
import { matchesResourceFilter } from 'app/resource_dashboard/resource_filter';
import { formatRate } from 'app/resource_dashboard/resource_velocity';
import { classifyResourceType } from 'app/side_panel/contents/queue_jobs_table/queue_jobs_pipes';

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
  readonly totalsLabel: string;
  readonly segments: readonly UserAllocationSegment[];
}

interface UserTotals {
  readonly user: string;
  readonly running: number;
  readonly pending: number;
}

/** Display units of each resource type, matching the backend's normalization. */
const RESOURCE_UNIT_LABELS: Readonly<Record<ResourceType, string>> = {
  [ResourceType.CPU]: 'cores',
  [ResourceType.MEMORY]: 'GiB',
  [ResourceType.GPU]: 'GPUs',
  [ResourceType.TPU]: 'chips',
  [ResourceType.UNKNOWN]: 'units',
};

/** Returns the display unit for a resource type, e.g. "GPUs" for "nvidia.com/gpu: nvidia-l4". */
export function resolveResourceUnitLabel(resourceType: string): string {
  return RESOURCE_UNIT_LABELS[classifyResourceType(resourceType)];
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
    .sort(
      (a, b) => b.running + b.pending - (a.running + a.pending) || a.user.localeCompare(b.user),
    );
}

function formatUnitsLabel(running: number, pending: number, unit: string): string {
  const runningText = `${formatRate(running)} running`;
  if (pending <= 0) return `${runningText} ${unit}`;
  return `${runningText} + ${formatRate(pending)} pending ${unit}`;
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
