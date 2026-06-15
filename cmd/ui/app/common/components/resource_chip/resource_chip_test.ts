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
import { setupModule, bootstrap } from 'xmanager_cloud/ui/app/testing/test';
import 'jasmine';

import { getHarness } from '@angular/cdk/testing/catalyst/async';
import { MatChipHarness } from '@angular/material/chips/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

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
    { name: 'GPU', type: ResourceType.GPU, expectedClass: 'green' },
  ];

  for (const tc of testCases) {
    it(`should apply correct classes for resource type ${tc.name}`, async () => {
      bootstrap(ResourceChip, { resourceType: tc.type, text: 'Some Text' });

      const chipHarness = await getHarness(MatChipHarness);
      const host = await chipHarness.host();

      expect(await host.hasClass(tc.expectedClass))
        .withContext(`Expected to have class "${tc.expectedClass}"`)
        .toBeTrue();
    });
  }
});
