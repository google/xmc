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

  it('should initialize with default amount and single empty resource type filter', async () => {
    expect(await harness.getAmountValue()).toBe('64');
    expect(await harness.getResourceTypeSelectCount()).toBe(1);
    expect(await harness.getRemoveButtonCount()).toBe(0);
  });

  it('should emit search query with default values when find button is clicked', async () => {
    expect(hostInstance.lastQuery()).toBeNull();

    await harness.clickFindButton();
    fixture.detectChanges();

    expect(hostInstance.lastQuery()).toEqual({ amount: 64, resourceTypes: [] });
  });

  it('should add a new resource type dropdown when + Add resource type is clicked', async () => {
    await harness.clickAddButton();
    fixture.detectChanges();

    expect(await harness.getResourceTypeSelectCount()).toBe(2);
    expect(await harness.getRemoveButtonCount()).toBe(2);

    await harness.clickAddButton();
    fixture.detectChanges();

    expect(await harness.getResourceTypeSelectCount()).toBe(3);
  });

  it('should remove a resource type dropdown when remove button is clicked', async () => {
    await harness.clickAddButton();
    fixture.detectChanges();

    expect(await harness.getResourceTypeSelectCount()).toBe(2);

    await harness.clickRemoveButton(0);
    fixture.detectChanges();

    expect(await harness.getResourceTypeSelectCount()).toBe(1);
    expect(await harness.getRemoveButtonCount()).toBe(0);
  });

  it('should update resource type filter selection and emit in query output', async () => {
    await harness.selectResourceType(0, 'nvidia.com/gpu');
    fixture.detectChanges();

    await harness.clickFindButton();
    fixture.detectChanges();

    expect(hostInstance.lastQuery()).toEqual({ amount: 64, resourceTypes: ['nvidia.com/gpu'] });
  });

  it('should update amount input and handle input changes', async () => {
    await harness.setAmountValue('128');
    fixture.detectChanges();

    await harness.clickFindButton();
    fixture.detectChanges();

    expect(hostInstance.lastQuery()).toEqual({ amount: 128, resourceTypes: [] });
  });

  it('should not emit query output when amount is zero or negative', async () => {
    await harness.setAmountValue('0');
    fixture.detectChanges();

    await harness.clickFindButton();
    fixture.detectChanges();

    expect(hostInstance.lastQuery()).toBeNull();
  });

  it('should deduplicate resource types when multiple dropdowns select the same type', async () => {
    await harness.clickAddButton();
    fixture.detectChanges();

    await harness.selectResourceType(0, 'nvidia.com/gpu');
    await harness.selectResourceType(1, 'nvidia.com/gpu');
    fixture.detectChanges();

    await harness.clickFindButton();
    fixture.detectChanges();

    expect(hostInstance.lastQuery()).toEqual({ amount: 64, resourceTypes: ['nvidia.com/gpu'] });
  });
});
