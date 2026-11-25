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
import { MatMenuHarness } from '@angular/material/menu/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { ExperimentStatus } from 'app/protos/experiment_state_server';
import {
  bootstrap,
  cleanState,
  flush,
  getEl,
  getEls,
  getHarness,
  hasEl,
  setupModule,
} from 'app/testing/test';

import { WorkUnitsStatusBar } from './work_units_status_bar';

const NO_WORK_UNITS_SELECTOR = '.no-work-units';
const STATE_SEGMENT_SELECTOR = '.states .state';
const MENU_ROW_SELECTOR = '.mat-mdc-menu-panel .key-value-pair';
const MENU_SQUARE_SELECTOR = '.square';
const MENU_LABEL_SELECTOR = '.state-count-state-container';
const MENU_COUNT_SELECTOR = 'dd';

@Component({
  standalone: true,
  imports: [WorkUnitsStatusBar],
  template: `<xmc-work-units-status-bar [stateCounts]="stateCounts()" />`,
})
class TestHostComponent {
  readonly stateCounts = signal<ExperimentStatus.StateCount[]>([]);
}

describe('WorkUnitsStatusBar', () => {
  cleanState(() => {
    setupModule({
      imports: [WorkUnitsStatusBar, TestHostComponent, NoopAnimationsModule],
    });
    return {};
  }, beforeEach);

  it('displays empty message when total work units count is zero', () => {
    bootstrap(WorkUnitsStatusBar, {
      stateCounts: [{ state: 'RUNNING', count: '0' }],
    });

    expect(hasEl(NO_WORK_UNITS_SELECTOR)).toBeTrue();
    expect(getEl(NO_WORK_UNITS_SELECTOR).textContent?.trim()).toBe('No Work Units');
    expect(getEls(STATE_SEGMENT_SELECTOR).length).toBe(0);
  });

  it('orders segments canonically, filters zero counts, and maps mapped and fallback classes', () => {
    bootstrap(WorkUnitsStatusBar, {
      stateCounts: [
        { state: 'FAILED', count: '2' },
        { state: 'PARTIALLY_FAILED', count: '1' },
        { state: 'COMPLETED', count: '0' },
        { state: 'WORK_UNIT_STATE_UNSPECIFIED', count: '1' },
        { state: 'RUNNING', count: '4' },
        { count: '2' },
      ],
    });

    const segments = getEls(STATE_SEGMENT_SELECTOR);
    expect(segments.length).toBe(5);

    expect(segments[0].classList.contains('WORK_UNIT_STATE_UNSPECIFIED')).toBeTrue();
    expect(segments[0].style.width).toBe('10%');

    expect(segments[1].classList.contains('unknown-state')).toBeTrue();
    expect(segments[1].classList.contains('undefined')).toBeFalse();
    expect(segments[1].style.width).toBe('20%');

    expect(segments[2].classList.contains('RUNNING')).toBeTrue();
    expect(segments[2].style.width).toBe('40%');

    expect(segments[3].classList.contains('PARTIALLY_FAILED')).toBeTrue();
    expect(segments[3].style.width).toBe('10%');

    expect(segments[4].classList.contains('FAILED')).toBeTrue();
    expect(segments[4].style.width).toBe('20%');
  });

  it('renders menu square classes and counts lazily inside mat-menu on open', async () => {
    bootstrap(WorkUnitsStatusBar, {
      stateCounts: [
        { state: 'RUNNING', count: '3' },
        { state: 'PARTIALLY_FAILED', count: '1' },
        { state: 'UNRECOGNIZED_STATE', count: '2' },
        { count: '1' },
      ],
    });

    const menu = await getHarness(MatMenuHarness);
    expect(document.querySelectorAll(MENU_ROW_SELECTOR).length).toBe(0);

    await menu.open();
    const rows = Array.from(document.querySelectorAll(MENU_ROW_SELECTOR));
    expect(rows.length).toBe(4);

    const rendered = rows.map((row) => {
      const square = row.querySelector(MENU_SQUARE_SELECTOR) as HTMLElement;
      const label = row.querySelector(MENU_LABEL_SELECTOR)?.textContent?.trim() ?? '';
      const count = row.querySelector(MENU_COUNT_SELECTOR)?.textContent?.trim() ?? '';
      return {
        classes: Array.from(square.classList).sort(),
        label,
        count,
      };
    });

    expect(rendered).toEqual([
      {
        classes: ['square', 'unknown-state'],
        label: 'work_unit_state_unspecified:',
        count: '1',
      },
      {
        classes: ['RUNNING', 'square'],
        label: 'running:',
        count: '3',
      },
      {
        classes: ['PARTIALLY_FAILED', 'square'],
        label: 'partially_failed:',
        count: '1',
      },
      {
        classes: ['square', 'unknown-state'],
        label: 'unrecognized_state:',
        count: '2',
      },
    ]);
  });

  it('updates rendered state segments dynamically when stateCounts signal changes', async () => {
    const hostComp = bootstrap(TestHostComponent);
    expect(hasEl(NO_WORK_UNITS_SELECTOR)).toBeTrue();

    hostComp.stateCounts.set([{ state: 'INITIALIZING', count: '2' }]);
    await flush();

    expect(hasEl(NO_WORK_UNITS_SELECTOR)).toBeFalse();
    const segments = getEls(STATE_SEGMENT_SELECTOR);
    expect(segments.length).toBe(1);
    expect(segments[0].classList.contains('INITIALIZING')).toBeTrue();
    expect(segments[0].style.width).toBe('100%');
  });
});
