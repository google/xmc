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
  readonly availableResourceTypes = signal<string[]>(['nvidia.com/gpu', 'cpu', 'tpu']);
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

  it('should initialize with default amount and single empty resource type filter', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const amountInput = document.querySelector('#amount-input') as HTMLInputElement;
    expect(amountInput.value).toBe('64');

    const selectElements = document.querySelectorAll('.query-select-box');
    expect(selectElements.length).toBe(1);

    const removeButtons = document.querySelectorAll('.remove-resource-btn');
    expect(removeButtons.length).toBe(0);
  });

  it('should emit search query with default values when find button is clicked', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const button = document.querySelector('.find-queues-action') as HTMLButtonElement;
    button.click();
    await flush();

    expect(host.lastQuery()).toEqual({ amount: 64, resourceTypes: [] });
  });

  it('should add a new resource type dropdown when + Add resource type is clicked', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const addButton = document.querySelector('.add-resource-btn') as HTMLButtonElement;
    addButton.click();
    await flush();

    let selectElements = document.querySelectorAll('.query-select-box');
    expect(selectElements.length).toBe(2);

    const removeButtons = document.querySelectorAll('.remove-resource-btn');
    expect(removeButtons.length).toBe(2);

    addButton.click();
    await flush();

    selectElements = document.querySelectorAll('.query-select-box');
    expect(selectElements.length).toBe(3);
  });

  it('should remove a resource type dropdown when remove button is clicked', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const addButton = document.querySelector('.add-resource-btn') as HTMLButtonElement;
    addButton.click();
    await flush();

    let selectElements = document.querySelectorAll('.query-select-box');
    expect(selectElements.length).toBe(2);

    const removeButtons = document.querySelectorAll('.remove-resource-btn');
    (removeButtons[0] as HTMLButtonElement).click();
    await flush();

    selectElements = document.querySelectorAll('.query-select-box');
    expect(selectElements.length).toBe(1);
    expect(document.querySelectorAll('.remove-resource-btn').length).toBe(0);
  });

  it('should update resource type filter selection and emit in query', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const selectElement = document.querySelector('.query-select-box') as HTMLSelectElement;
    selectElement.value = 'nvidia.com/gpu';
    selectElement.dispatchEvent(new Event('change'));
    await flush();

    const button = document.querySelector('.find-queues-action') as HTMLButtonElement;
    button.click();
    await flush();

    expect(host.lastQuery()).toEqual({ amount: 64, resourceTypes: ['nvidia.com/gpu'] });
  });

  it('should update amount input and handle input changes', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const amountInput = document.querySelector('#amount-input') as HTMLInputElement;
    amountInput.value = '128';
    amountInput.dispatchEvent(new Event('input'));
    amountInput.dispatchEvent(new Event('change'));
    await flush();

    const button = document.querySelector('.find-queues-action') as HTMLButtonElement;
    button.click();
    await flush();

    expect(host.lastQuery()).toEqual({ amount: 128, resourceTypes: [] });
  });

  it('should not emit query when amount is zero or negative', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const amountInput = document.querySelector('#amount-input') as HTMLInputElement;
    amountInput.value = '0';
    amountInput.dispatchEvent(new Event('input'));
    amountInput.dispatchEvent(new Event('change'));
    await flush();

    const button = document.querySelector('.find-queues-action') as HTMLButtonElement;
    button.click();
    await flush();

    expect(host.lastQuery()).toBeNull();
  });

  it('should deduplicate resource types when multiple dropdowns select the same type', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const addButton = document.querySelector('.add-resource-btn') as HTMLButtonElement;
    addButton.click();
    await flush();

    const selectElements = document.querySelectorAll('.query-select-box');
    (selectElements[0] as HTMLSelectElement).value = 'nvidia.com/gpu';
    selectElements[0].dispatchEvent(new Event('change'));

    (selectElements[1] as HTMLSelectElement).value = 'nvidia.com/gpu';
    selectElements[1].dispatchEvent(new Event('change'));
    await flush();

    const button = document.querySelector('.find-queues-action') as HTMLButtonElement;
    button.click();
    await flush();

    expect(host.lastQuery()).toEqual({ amount: 64, resourceTypes: ['nvidia.com/gpu'] });
  });
});
