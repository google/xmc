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
import { getHarness } from '@angular/cdk/testing/catalyst/async';
import { TestBed } from '@angular/core/testing';
import { MatChipHarness } from '@angular/material/chips/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { bootstrap, setupModule } from 'app/testing/test';

import { StatusChip, StatusOption } from './status_chip';

describe('StatusChip Component', () => {
  beforeEach(() => {
    setupModule({
      imports: [StatusChip, NoopAnimationsModule],
    });
  });

  it('displays status with optional replica count', async () => {
    bootstrap(StatusChip, { status: StatusOption.RUNNING, replicas: 4 });

    const chipHarness = await getHarness(MatChipHarness);
    expect(await chipHarness.getText()).toBe('4 Running');
  });

  const expectedColors: ReadonlyArray<{ status: StatusOption; expectedClass: string }> = [
    { status: StatusOption.SUCCESS, expectedClass: 'green' },
    { status: StatusOption.COMPLETED, expectedClass: 'green' },
    { status: StatusOption.STARTING, expectedClass: 'light-green' },
    { status: StatusOption.FAILED, expectedClass: 'red' },
    { status: StatusOption.RUNNING, expectedClass: 'blue' },
    { status: StatusOption.NOT_RUNNING, expectedClass: 'blue' },
    { status: StatusOption.PENDING, expectedClass: 'yellow' },
    { status: StatusOption.INITIALIZING, expectedClass: 'yellow' },
    { status: StatusOption.UNKNOWN, expectedClass: 'yellow' },
    { status: StatusOption.RESTARTING, expectedClass: 'yellow' },
    { status: StatusOption.DESCHEDULING, expectedClass: 'grey' },
    { status: StatusOption.SCHEDULING, expectedClass: 'grey' },
    { status: StatusOption.SCHEDULING_PAUSED, expectedClass: 'orange' },
    { status: StatusOption.STOPPING, expectedClass: 'black' },
    { status: StatusOption.STOPPED, expectedClass: 'black' },
  ];

  for (const tc of expectedColors) {
    it(`applies ${tc.expectedClass} class and keeps mat-mdc-chip for ${tc.status}`, async () => {
      bootstrap(StatusChip, { status: tc.status });

      const chipHarness = await getHarness(MatChipHarness);
      const host = await chipHarness.host();

      expect(await host.hasClass('mat-mdc-chip'))
        .withContext('Expected host Material chip class to be preserved')
        .toBeTrue();
      expect(await host.hasClass(tc.expectedClass))
        .withContext(`Expected status "${tc.status}" to apply class "${tc.expectedClass}"`)
        .toBeTrue();
    });
  }

  it('updates chip color class dynamically when status changes without dropping host classes', () => {
    const fixture = TestBed.createComponent(StatusChip);
    fixture.componentRef.setInput('status', StatusOption.RUNNING);
    fixture.detectChanges();

    const chipEl = fixture.nativeElement.querySelector('mat-chip') as HTMLElement;
    expect(chipEl.classList.contains('mat-mdc-chip')).toBeTrue();
    expect(chipEl.classList.contains('blue')).toBeTrue();

    fixture.componentRef.setInput('status', StatusOption.COMPLETED);
    fixture.detectChanges();

    expect(chipEl.classList.contains('mat-mdc-chip')).toBeTrue();
    expect(chipEl.classList.contains('blue')).toBeFalse();
    expect(chipEl.classList.contains('green')).toBeTrue();
  });
});
