// Copyright 2025 Google LLC
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
import { getHarness } from '@angular/cdk/testing/catalyst/async';
import { TestBed } from '@angular/core/testing';
import { MatChipHarness } from '@angular/material/chips/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { bootstrap, setupModule } from 'app/testing/test';

import { ResourceChip, ResourceType } from './resource_chip';

describe('ResourceChip Component', () => {
  beforeEach(() => {
    setupModule({
      imports: [ResourceChip, NoopAnimationsModule],
    });
  });

  it('should display the correct text inside the chip', async () => {
    const testText = 'CPU Usage: 80%';
    bootstrap(ResourceChip, { resourceType: ResourceType.CPU, text: testText });

    const chipHarness = await getHarness(MatChipHarness);
    expect(await chipHarness.getText()).toBe(testText);
  });

  const testCases = [
    { name: 'CPU', type: ResourceType.CPU, expectedClass: 'grey' },
    { name: 'MEMORY', type: ResourceType.MEMORY, expectedClass: 'grey' },
    { name: 'UNKNOWN', type: ResourceType.UNKNOWN, expectedClass: 'grey' },
    { name: 'GPU', type: ResourceType.GPU, expectedClass: 'green' },
    { name: 'TPU', type: ResourceType.TPU, expectedClass: 'green' },
  ];

  for (const tc of testCases) {
    it(`should apply correct classes for resource type ${tc.name}`, async () => {
      bootstrap(ResourceChip, { resourceType: tc.type, text: 'Some Text' });

      const chipHarness = await getHarness(MatChipHarness);
      const host = await chipHarness.host();

      expect(await host.hasClass('mat-mdc-chip'))
        .withContext('Expected host Material chip class to be preserved')
        .toBeTrue();
      expect(await host.hasClass(tc.expectedClass))
        .withContext(`Expected to have class "${tc.expectedClass}"`)
        .toBeTrue();
    });
  }

  it('updates chip class cleanly when resourceType changes without dropping host classes', () => {
    const fixture = TestBed.createComponent(ResourceChip);
    fixture.componentRef.setInput('resourceType', ResourceType.CPU);
    fixture.componentRef.setInput('text', 'CPU');
    fixture.detectChanges();

    const chipEl = fixture.nativeElement.querySelector('mat-chip') as HTMLElement;
    expect(chipEl.classList.contains('mat-mdc-chip')).toBeTrue();
    expect(chipEl.classList.contains('grey')).toBeTrue();

    fixture.componentRef.setInput('resourceType', ResourceType.GPU);
    fixture.detectChanges();

    expect(chipEl.classList.contains('mat-mdc-chip')).toBeTrue();
    expect(chipEl.classList.contains('grey')).toBeFalse();
    expect(chipEl.classList.contains('green')).toBeTrue();
  });
});
