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

import { SchedulingPauseModalComponent } from './scheduling_pause_modal';
import { SchedulingPauseModalHarness } from './testing/scheduling_pause_modal_harness';

@Component({
  standalone: true,
  imports: [SchedulingPauseModalComponent],
  template: `
    <xmc-scheduling-pause-modal
      [isPausing]="isPausing()"
      (confirm)="onConfirm()"
      (cancel)="onCancel()"
    />
  `,
})
class TestModalHostComponent {
  readonly isPausing = signal<boolean>(true);
  confirmCount = 0;
  cancelCount = 0;

  onConfirm(): void {
    this.confirmCount++;
  }

  onCancel(): void {
    this.cancelCount++;
  }
}

describe('SchedulingPauseModalComponent', () => {
  cleanState(() => {
    setupModule({
      imports: [TestModalHostComponent, NoopAnimationsModule],
    });
  }, beforeEach);

  it('should display pause confirmation text when isPausing is true', async () => {
    const host = bootstrap(TestModalHostComponent);
    host.isPausing.set(true);
    await flush();

    const harness = await getHarness(SchedulingPauseModalHarness);
    expect(await harness.getTitleText()).toBe('Confirm Action');
    expect(await harness.getMessageText()).toContain('pause');
    expect(await harness.getMessageText()).toContain(
      'This will stop the actuator from interacting with the control plane.',
    );
    expect(await harness.getConfirmButtonText()).toBe('Yes, Pause');
    expect(await harness.isConfirmPauseButton()).toBeTrue();
  });

  it('should display resume confirmation text when isPausing is false', async () => {
    const host = bootstrap(TestModalHostComponent);
    host.isPausing.set(false);
    await flush();

    const harness = await getHarness(SchedulingPauseModalHarness);
    expect(await harness.getMessageText()).toContain('resume');
    expect(await harness.getMessageText()).toContain(
      'This will resume normal scheduling operations.',
    );
    expect(await harness.getConfirmButtonText()).toBe('Yes, Resume');
    expect(await harness.isConfirmResumeButton()).toBeTrue();
  });

  it('should emit confirm when clicking confirm button', async () => {
    const host = bootstrap(TestModalHostComponent);
    await flush();

    const harness = await getHarness(SchedulingPauseModalHarness);
    await harness.clickConfirm();
    await flush();

    expect(host.confirmCount).toBe(1);
    expect(host.cancelCount).toBe(0);
  });

  it('should emit cancel when clicking cancel button', async () => {
    const host = bootstrap(TestModalHostComponent);
    await flush();

    const harness = await getHarness(SchedulingPauseModalHarness);
    await harness.clickCancel();
    await flush();

    expect(host.cancelCount).toBe(1);
    expect(host.confirmCount).toBe(0);
  });

  it('should emit cancel when clicking backdrop', async () => {
    const host = bootstrap(TestModalHostComponent);
    await flush();

    const harness = await getHarness(SchedulingPauseModalHarness);
    await harness.clickBackdrop();
    await flush();

    expect(host.cancelCount).toBe(1);
  });

  it('should emit cancel when Escape key is pressed', async () => {
    const host = bootstrap(TestModalHostComponent);
    await flush();

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await flush();

    expect(host.cancelCount).toBe(1);
  });
});
