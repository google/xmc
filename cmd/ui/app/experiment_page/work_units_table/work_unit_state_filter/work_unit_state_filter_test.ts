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
import { CheckboxHarnessFilters, MatCheckboxHarness } from '@angular/material/checkbox/testing';
import { MatMenuHarness } from '@angular/material/menu/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { FilterMenu } from 'app/common/components/filter_menu/filter_menu';
import { bootstrapTemplate, flush, getHarness, setupModule } from 'app/testing/test';

import { WorkUnitStateFilter } from './work_unit_state_filter';

interface HostContext {
  selected: readonly string[];
  emitted: Array<readonly string[]>;
}

/** Matches state checkboxes, optionally narrowed by label or checked state. */
function stateOption(filters: CheckboxHarnessFilters = {}) {
  return MatCheckboxHarness.with({ ...filters, selector: '.state-filter-option' });
}

/** Hosts the filter in a filter menu, as the work units table does. */
function bootstrapFilter(selected: readonly string[] = []): HostContext {
  const context: HostContext = { selected, emitted: [] };
  bootstrapTemplate(
    `<xmc-filter-menu label="Filters">
      <xmc-work-unit-state-filter
        [selectedStates]="selected"
        (selectedStatesChange)="emitted.push($event)"
      />
    </xmc-filter-menu>`,
    context,
  );
  return context;
}

async function toggleStates(menu: MatMenuHarness, ...labels: string[]): Promise<void> {
  await menu.open();
  for (const label of labels) {
    await (await menu.getHarness(stateOption({ label }))).toggle();
  }
  await menu.close();
}

describe('WorkUnitStateFilter', () => {
  beforeEach(() => {
    setupModule({ imports: [FilterMenu, WorkUnitStateFilter, NoopAnimationsModule] });
  });

  it('should show the applied selection as checked', async () => {
    bootstrapFilter(['FAILED']);
    const menu: MatMenuHarness = await getHarness(MatMenuHarness);

    await menu.open();

    const checked = await menu.getAllHarnesses(stateOption({ checked: true }));
    expect(await Promise.all(checked.map((c) => c.getLabelText()))).toEqual(['Failed']);
  });

  it('should emit the new selection once, in option order, when the menu closes', async () => {
    const context = bootstrapFilter();
    const menu: MatMenuHarness = await getHarness(MatMenuHarness);

    await toggleStates(menu, 'Failed', 'Running');

    expect(context.emitted).toEqual([['RUNNING', 'FAILED']]);
  });

  it('should not emit when the selection is unchanged on close', async () => {
    const context = bootstrapFilter(['STOPPED']);
    const menu: MatMenuHarness = await getHarness(MatMenuHarness);

    await toggleStates(menu);
    await toggleStates(menu, 'Running', 'Running');

    expect(context.emitted).toEqual([]);
  });

  it('should not overwrite a selection changed externally while the menu is open', async () => {
    const context = bootstrapFilter(['STOPPED']);
    const menu: MatMenuHarness = await getHarness(MatMenuHarness);

    await menu.open();
    context.selected = ['FAILED'];
    await flush();
    await menu.close();

    expect(context.emitted).toEqual([]);
  });
});
