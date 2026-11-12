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

import { Component, signal } from '@angular/core';

import { UserResourceAllocation } from 'app/protos/rod';
import { bootstrap, flush, getHarness, setupModule } from 'app/testing/test';

import { QueueUserAllocationBar } from './queue_user_allocation_bar';
import { QueueUserAllocationBarHarness } from './queue_user_allocation_bar_harness';

const ALLOCATIONS: UserResourceAllocation[] = [
  {
    user: 'alice@example.com',
    resourceType: 'nvidia.com/gpu: nvidia-tesla-h100',
    runningUnits: 4,
    pendingUnits: 2,
  },
  { user: 'alice@example.com', resourceType: 'nvidia.com/gpu: nvidia-l4', runningUnits: 2 },
  { user: 'bob@example.com', resourceType: 'nvidia.com/gpu: nvidia-tesla-h100', runningUnits: 2 },
];

@Component({
  standalone: true,
  imports: [QueueUserAllocationBar],
  template: `
    <xmc-queue-user-allocation-bar
      [allocations]="allocations()"
      [dominantResource]="dominantResource()"
      [selectedAuthor]="selectedAuthor()"
      (authorSelect)="onAuthorSelect($event)"
    />
  `,
})
class TestHostComponent {
  readonly allocations = signal<UserResourceAllocation[]>(ALLOCATIONS);
  readonly dominantResource = signal<string | null>(null);
  readonly selectedAuthor = signal<string>('');
  readonly selectedAuthors: string[] = [];

  onAuthorSelect(author: string): void {
    this.selectedAuthors.push(author);
  }
}

describe('QueueUserAllocationBar', () => {
  beforeEach(() => {
    setupModule({ imports: [TestHostComponent] });
  });

  async function render(dominantResource: string | null): Promise<QueueUserAllocationBarHarness> {
    const host: TestHostComponent = bootstrap(TestHostComponent);
    host.dominantResource.set(dominantResource);
    await flush();
    return getHarness(QueueUserAllocationBarHarness);
  }

  it('shows the hint and hides the bar when the picker is empty', async () => {
    const bar = await render(null);
    expect(await bar.getHintText()).toContain('Pick a resource in "I need"');
    expect(await bar.hasBar()).toBeFalse();
    expect(await bar.getChipTexts()).toEqual([]);
  });

  it('shows per-user running and pending units for a specific model', async () => {
    const bar = await render('nvidia.com/gpu: nvidia-tesla-h100');
    expect(await bar.getTitleText()).toBe('Who holds nvidia.com/gpu: nvidia-tesla-h100?');
    expect(await bar.getTotalsText()).toBe('6 running + 2 pending GPUs');
    expect(await bar.getChipTexts()).toEqual([
      'alice@example.com 4 running + 2 pending GPUs',
      'bob@example.com 2 running GPUs',
    ]);
    expect(await bar.getRunningSegmentWidths()).toEqual(['50%', '25%']);
  });

  it('shows an empty message when no active job requests the resource', async () => {
    const bar = await render('google.com/tpu');
    expect(await bar.hasBar()).toBeFalse();
    expect(await bar.getEmptyText()).toBe('No active jobs request google.com/tpu in this queue.');
  });

  it('emits clicked author when a user chip is clicked', async () => {
    const host: TestHostComponent = bootstrap(TestHostComponent);
    host.dominantResource.set('nvidia.com/gpu: nvidia-tesla-h100');
    await flush();
    const bar = await getHarness(QueueUserAllocationBarHarness);

    await bar.clickUserChip('alice@example.com');
    expect(host.selectedAuthors).toEqual(['alice@example.com']);
  });

  it('marks the matching chip as selected when selectedAuthor is set', async () => {
    const host: TestHostComponent = bootstrap(TestHostComponent);
    host.dominantResource.set('nvidia.com/gpu: nvidia-tesla-h100');
    host.selectedAuthor.set('alice@example.com');
    await flush();
    const bar = await getHarness(QueueUserAllocationBarHarness);

    expect(await bar.isUserChipSelected('alice@example.com')).toBeTrue();
    expect(await bar.isUserChipSelected('bob@example.com')).toBeFalse();
  });
});
