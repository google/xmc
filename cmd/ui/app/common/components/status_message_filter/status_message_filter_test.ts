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
import { MatBadgeHarness } from '@angular/material/badge/testing';
import {
  CheckboxHarnessFilters,
  MatCheckboxHarness,
} from '@angular/material/checkbox/testing';
import { MatMenuHarness } from '@angular/material/menu/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { BehaviorSubject } from 'rxjs';

import { StructuredMessage } from 'app/protos/experiment_state_server';
import { StatusMessageFilterService } from 'app/service/status_message_filter_service';
import { bootstrap, cleanState, getHarness, setupModule } from 'app/testing/test';

import { StateFilterOption, StatusMessageFilter } from './status_message_filter';

const STATE_OPTIONS: readonly StateFilterOption[] = [
  { value: 'RUNNING', label: 'Running' },
  { value: 'STOPPED', label: 'Stopped' },
  { value: 'FAILED', label: 'Failed' },
];

/** Matches checkboxes of the Status block, optionally narrowed by label or checked state. */
function stateOption(filters: CheckboxHarnessFilters = {}) {
  return MatCheckboxHarness.with({ ...filters, selector: '.state-filter-option' });
}

describe('StatusMessageFilter', () => {
  cleanState(() => {
    const filterService = jasmine.createSpyObj<StatusMessageFilterService>(
      'StatusMessageFilterService',
      ['updateSeverities', 'updateSources'],
      {
        severities$: new BehaviorSubject(new Set<StructuredMessage.Severity>()),
        sources$: new BehaviorSubject(new Set<StructuredMessage.Source>()),
      },
    );
    setupModule({
      imports: [StatusMessageFilter, NoopAnimationsModule],
      providers: [{ provide: StatusMessageFilterService, useValue: filterService }],
    });
    return { filterService };
  }, beforeEach);

  async function toggleStates(menu: MatMenuHarness, ...labels: string[]): Promise<void> {
    await menu.open();
    for (const label of labels) {
      await (await menu.getHarness(stateOption({ label }))).toggle();
    }
    await menu.close();
  }

  it('should not show the Status block without state options', async () => {
    bootstrap(StatusMessageFilter);
    const menu = await getHarness(MatMenuHarness);

    expect(await (await menu.host()).getAttribute('aria-label')).toBe('Filter status messages');
    await menu.open();
    expect(await menu.getAllHarnesses(stateOption())).toEqual([]);
    expect(await (await getHarness(MatBadgeHarness)).isHidden()).toBeTrue();
  });

  it('should show the applied selection as checked and on the badge', async () => {
    bootstrap(StatusMessageFilter, { stateOptions: STATE_OPTIONS, selectedStates: ['FAILED'] });
    const menu = await getHarness(MatMenuHarness);

    expect(await (await menu.host()).getAttribute('aria-label')).toBe('Filters');
    expect(await (await getHarness(MatBadgeHarness)).getText()).toBe('1');
    await menu.open();
    const checked = await menu.getAllHarnesses(stateOption({ checked: true }));
    expect(await Promise.all(checked.map((c) => c.getLabelText()))).toEqual(['Failed']);
  });

  it('should emit the new selection once, in option order, when the menu closes', async () => {
    const comp = bootstrap(StatusMessageFilter, { stateOptions: STATE_OPTIONS });
    const emitted: Array<readonly string[]> = [];
    comp.selectedStatesChange.subscribe((states: readonly string[]) => emitted.push(states));
    const menu = await getHarness(MatMenuHarness);

    await toggleStates(menu, 'Failed', 'Running');

    expect(emitted).toEqual([['RUNNING', 'FAILED']]);
  });

  it('should not emit when the selection is unchanged on close', async () => {
    const comp = bootstrap(StatusMessageFilter, {
      stateOptions: STATE_OPTIONS,
      selectedStates: ['STOPPED'],
    });
    const emitted: Array<readonly string[]> = [];
    comp.selectedStatesChange.subscribe((states: readonly string[]) => emitted.push(states));
    const menu = await getHarness(MatMenuHarness);

    await toggleStates(menu);
    await toggleStates(menu, 'Running', 'Running');

    expect(emitted).toEqual([]);
  });
});
