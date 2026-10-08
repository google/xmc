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
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import {
  bootstrap,
  cleanState,
  flush,
  getHarness,
  setupModule,
} from 'xmanager_cloud/ui/app/testing/test';

import { SchedulingPauseButtonComponent } from './scheduling_pause_button';
import { SchedulingPauseButtonHarness } from './testing/scheduling_pause_button_harness';

@Component({
  standalone: true,
  imports: [SchedulingPauseButtonComponent],
  template: `
    <xmc-scheduling-pause-button
      [isSchedulingPaused]="isSchedulingPaused()"
      [disabled]="disabled()"
      (toggleClick)="onToggle()"
    />
  `,
})
class TestHostComponent {
  readonly isSchedulingPaused = signal<boolean>(false);
  readonly disabled = signal<boolean>(false);
  toggleCallCount = 0;

  onToggle(): void {
    this.toggleCallCount++;
  }
}

describe('SchedulingPauseButtonComponent', () => {
  cleanState(() => {
    setupModule({
      imports: [TestHostComponent, NoopAnimationsModule],
    });
  }, beforeEach);

  it('should render in unpaused state by default', async () => {
    bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(SchedulingPauseButtonHarness);
    expect(await harness.isPaused()).toBeFalse();
    expect(await harness.isChecked()).toBeFalse();
    expect(await harness.isDisabled()).toBeFalse();
    expect(await harness.getTitle()).toBe('Pause All Scheduling');
    expect(await harness.getSubtitle()).toContain('Temporarily stop actuator');
    expect(await harness.getIconName()).toContain('play_circle');
  });

  it('should render paused styles and icon when isSchedulingPaused is true', async () => {
    const host = bootstrap(TestHostComponent);
    host.isSchedulingPaused.set(true);
    await flush();

    const harness = await getHarness(SchedulingPauseButtonHarness);
    expect(await harness.isPaused()).toBeTrue();
    expect(await harness.isChecked()).toBeTrue();
    expect(await harness.getIconName()).toContain('pause_circle');
  });

  it('should emit toggleClick and not toggle state automatically when clicked', async () => {
    const host = bootstrap(TestHostComponent);
    await flush();

    const harness = await getHarness(SchedulingPauseButtonHarness);
    expect(await harness.isChecked()).toBeFalse();

    await harness.toggle();
    await flush();

    expect(host.toggleCallCount).toBe(1);
    expect(await harness.isChecked()).toBeFalse();
  });

  it('should disable toggle when disabled input is true', async () => {
    const host = bootstrap(TestHostComponent);
    host.disabled.set(true);
    await flush();

    const harness = await getHarness(SchedulingPauseButtonHarness);
    expect(await harness.isDisabled()).toBeTrue();
  });
});
