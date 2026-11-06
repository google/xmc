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

import {
  formatAllocationUnits,
  resolveResourceUnitLabel,
  summarizeUserAllocations,
} from './user_allocation_summary';

const ALLOCATIONS: UserResourceAllocation[] = [
  {
    user: 'alice@example.com',
    resourceType: 'nvidia.com/gpu: nvidia-tesla-h100',
    runningUnits: 4,
    pendingUnits: 2,
  },
  { user: 'alice@example.com', resourceType: 'nvidia.com/gpu: nvidia-l4', runningUnits: 2 },
  { user: 'bob@example.com', resourceType: 'nvidia.com/gpu: nvidia-tesla-h100', runningUnits: 2 },
  { user: '', resourceType: 'cpu', runningUnits: 1.5 },
  { user: 'bob@example.com', resourceType: 'memory', pendingUnits: 0.5 },
];

describe('user_allocation_summary', () => {
  it('resolves display units per resource type', () => {
    expect(resolveResourceUnitLabel('memory')).toBe('GiB');
    expect(resolveResourceUnitLabel('cpu')).toBe('cores');
    expect(resolveResourceUnitLabel('google.com/tpu: tpu-v5-lite-podslice')).toBe('chips');
    expect(resolveResourceUnitLabel('nvidia.com/gpu')).toBe('GPUs');
    expect(resolveResourceUnitLabel('example.com/fpga')).toBe('units');
  });

  it('formats units with at most two decimals', () => {
    expect(formatAllocationUnits(8)).toBe('8');
    expect(formatAllocationUnits(0.5)).toBe('0.5');
    expect(formatAllocationUnits(2.256)).toBe('2.26');
  });

  it('returns null without a dominant resource or matching allocations', () => {
    expect(summarizeUserAllocations(ALLOCATIONS, null)).toBeNull();
    expect(summarizeUserAllocations(ALLOCATIONS, 'google.com/tpu')).toBeNull();
  });

  it('sums all matching models for a generic parent, ordered by total', () => {
    const summary = summarizeUserAllocations(ALLOCATIONS, 'nvidia.com/gpu')!;
    expect(summary.totalsLabel).toBe('8 running + 2 pending GPUs');
    expect(summary.segments.map((s) => [s.user, s.label])).toEqual([
      ['alice@example.com', '6 running + 2 pending GPUs'],
      ['bob@example.com', '2 running GPUs'],
    ]);
    expect(summary.segments[0].runningPercent).toBeCloseTo(60);
    expect(summary.segments[0].pendingPercent).toBeCloseTo(20);
  });

  it('labels unknown users and non-accelerator resources', () => {
    const cpu = summarizeUserAllocations(ALLOCATIONS, 'cpu')!;
    expect(cpu.segments.map((s) => [s.user, s.label])).toEqual([['unknown', '1.5 running cores']]);
    const memory = summarizeUserAllocations(ALLOCATIONS, 'memory')!;
    expect(memory.totalsLabel).toBe('0 running + 0.5 pending GiB');
  });
});
