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

import 'jasmine';

import { Component, signal } from '@angular/core';

import {
  bootstrap,
  cleanState,
  flush,
  setupModule,
  setupObservableMatchers,
} from 'xmanager_cloud/ui/app/testing/test';

import { ResourceRequirementQuery, SmartResourceFinderComponent } from './smart_resource_finder';

@Component({
  standalone: true,
  imports: [SmartResourceFinderComponent],
  template: `
    <xmc-smart-resource-finder
      [availableResourceTypes]="availableResourceTypes()"
      (findMatchingQueues)="onFind($event)"
    />
  `,
})
class TestHostComponent {
  readonly availableResourceTypes = signal<string[]>(['nvidia.com/gpu', 'cpu']);
  readonly lastQuery = signal<ResourceRequirementQuery | null>(null);

  onFind(query: ResourceRequirementQuery) {
    this.lastQuery.set(query);
  }
}

describe('SmartResourceFinderComponent', () => {
  beforeAll(setupObservableMatchers);

  cleanState(() => {
    setupModule({
      imports: [TestHostComponent],
    });
    return {};
  }, beforeEach);

  it('should initialize and emit search query on find action', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const button = document.querySelector('.find-queues-action') as HTMLButtonElement;
    button?.click();
    await flush();

    expect(host.lastQuery()).toEqual({ amount: 64, resourceTypes: [] });
  });
});
