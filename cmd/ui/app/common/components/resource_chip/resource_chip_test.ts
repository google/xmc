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
import 'jasmine';

import {getHarness} from '@angular/cdk/testing/catalyst/async';
import {MatChipHarness} from '@angular/material/chips/testing';
import {NoopAnimationsModule} from '@angular/platform-browser/animations';

import {ResourceChip, ResourceType} from './resource_chip';

describe('ResourceChip Component', () => {
  beforeEach(() => {
    setupModule({
      imports: [
        ResourceChip,
        NoopAnimationsModule,
      ],
    });
  });

  it('should display the correct text inside the chip', async () => {
    const testText = 'CPU Usage: 80%';
    bootstrap(ResourceChip, {resourceType: ResourceType.CPU, text: testText});

    const chipHarness = await getHarness(MatChipHarness);
    expect(await chipHarness.getText()).toBe(testText);
  });

  pit(
      'should apply correct classes for',
      {
        'CPU': {
          type: ResourceType.CPU,
          expectedClass: 'grey',
        },
        'MEMORY': {
          type: ResourceType.MEMORY,
          expectedClass: 'grey',
        },
        'GPU': {
          type: ResourceType.GPU,
          expectedClass: 'green',
        },
      },
      'resource type',
      async ({type, expectedClass}:
                 {type: ResourceType, expectedClass: string}) => {
        bootstrap(ResourceChip, {resourceType: type, text: 'Some Text'});

        const chipHarness = await getHarness(MatChipHarness);
        const host = await chipHarness.host();

        expect(await host.hasClass(expectedClass))
            .withContext(`Expected to have class "${expectedClass}"`)
            .toBeTrue();
      },
  );
});
