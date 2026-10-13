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

import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { TestBed } from '@angular/core/testing';

import { SchedulingPausedBanner } from './scheduling_paused_banner';
import { SchedulingPausedBannerHarness } from './scheduling_paused_banner_harness';

describe('SchedulingPausedBanner', () => {
  let harness: SchedulingPausedBannerHarness;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [SchedulingPausedBanner],
    }).compileComponents();

    const fixture = TestBed.createComponent(SchedulingPausedBanner);
    fixture.detectChanges();
    harness = await TestbedHarnessEnvironment.harnessForFixture(
      fixture,
      SchedulingPausedBannerHarness,
    );
  });

  it('should explain that scheduling is paused until an administrator resumes it', async () => {
    const text = await harness.getText();

    expect(text).toContain('Scheduling is paused.');
    expect(text).toContain('until an administrator resumes scheduling');
  });

  it('should expose the banner as a status region for assistive technology', async () => {
    expect(await harness.getRole()).toBe('status');
  });
});
