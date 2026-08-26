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

import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { Component, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { ResourceRequirementQuery, SmartResourceFinderComponent } from './smart_resource_finder';
import { SmartResourceFinderHarness } from './smart_resource_finder_harness';

@Component({
  standalone: true,
  imports: [SmartResourceFinderComponent],
  template: `
    <xmc-smart-resource-finder
      [availableResourceTypes]="['cpu', 'nvidia.com/gpu', 'tpu']"
      (findMatchingQueues)="lastQuery.set($event)"
    />
  `,
})
class TestHostComponent {
  readonly lastQuery = signal<ResourceRequirementQuery | null>(null);
}

describe('SmartResourceFinderComponent', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let hostInstance: TestHostComponent;
  let harness: SmartResourceFinderHarness;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TestHostComponent, NoopAnimationsModule],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    fixture.detectChanges();
    hostInstance = fixture.componentInstance;
    const loader = TestbedHarnessEnvironment.loader(fixture);
    harness = await loader.getHarness(SmartResourceFinderHarness);
  });

  it('should initialize with default amount 1 and single empty resource type filter and emit initial query', async () => {
    expect(await harness.getAmountValue()).toBe('1');
    expect(await harness.getResourceTypeSelectCount()).toBe(1);
    expect(await harness.getRemoveButtonCount()).toBe(0);
    expect(hostInstance.lastQuery()).toEqual({
      requirements: [jasmine.objectContaining({ amount: 1, resourceType: '' })],
    });
  });

  it('should add a new row when + Add resource type is clicked and emit live query', async () => {
    await harness.clickAddButton();
    fixture.detectChanges();

    expect(await harness.getResourceTypeSelectCount()).toBe(2);
    expect(await harness.getRemoveButtonCount()).toBe(2);
    expect(hostInstance.lastQuery()).toEqual({
      requirements: [
        jasmine.objectContaining({ amount: 1, resourceType: '' }),
        jasmine.objectContaining({ amount: 1, resourceType: '' }),
      ],
    });
  });

  it('should remove a specific resource filter row by index and emit live query', async () => {
    await harness.clickAddButton(); // index 1
    await harness.clickAddButton(); // index 2
    fixture.detectChanges();

    await harness.selectResourceType(0, 'nvidia.com/gpu');
    await harness.selectResourceType(1, 'cpu');
    await harness.selectResourceType(2, 'tpu');
    fixture.detectChanges();

    // Remove middle filter ('cpu' at index 1)
    await harness.clickRemoveButton(1);
    fixture.detectChanges();

    expect(await harness.getResourceTypeSelectCount()).toBe(2);
    expect(hostInstance.lastQuery()).toEqual({
      requirements: [
        jasmine.objectContaining({ amount: 1, resourceType: 'nvidia.com/gpu' }),
        jasmine.objectContaining({ amount: 1, resourceType: 'tpu' }),
      ],
    });
  });

  it('should update resource type filter selection live', async () => {
    await harness.selectResourceType(0, 'nvidia.com/gpu');
    fixture.detectChanges();

    expect(hostInstance.lastQuery()).toEqual({
      requirements: [jasmine.objectContaining({ amount: 1, resourceType: 'nvidia.com/gpu' })],
    });
  });

  it('should update amount input live', async () => {
    await harness.setAmountValue('128');
    fixture.detectChanges();

    expect(hostInstance.lastQuery()).toEqual({
      requirements: [jasmine.objectContaining({ amount: 128, resourceType: '' })],
    });
  });

  it('should exclude row when amount is zero or negative', async () => {
    await harness.setAmountValue('0');
    fixture.detectChanges();

    expect(hostInstance.lastQuery()).toEqual({ requirements: [] });
  });
});
