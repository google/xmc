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
import { MatMenuHarness } from '@angular/material/menu/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { bootstrapTemplate, getHarness, setupModule } from 'app/testing/test';

import { FilterMenu } from './filter_menu';

interface HostContext {
  count: number;
  events: string[];
}

function bootstrapMenu(count = 0): HostContext {
  const context: HostContext = { count, events: [] };
  bootstrapTemplate(
    `<xmc-filter-menu
      label="Filters"
      [activeFilterCount]="count"
      (opened)="events.push('opened')"
      (closed)="events.push('closed')"
    >
      <div class="projected-section">Section</div>
    </xmc-filter-menu>`,
    context,
  );
  return context;
}

describe('FilterMenu', () => {
  beforeEach(() => {
    setupModule({ imports: [FilterMenu, NoopAnimationsModule] });
  });

  it('should label the trigger and hide the badge without applied filters', async () => {
    bootstrapMenu();
    const menu = await getHarness(MatMenuHarness);

    expect(await (await menu.host()).getAttribute('aria-label')).toBe('Filters');
    expect(await (await getHarness(MatBadgeHarness)).isHidden()).toBeTrue();
  });

  it('should show the number of applied filters on the badge', async () => {
    bootstrapMenu(2);

    const badge = await getHarness(MatBadgeHarness);
    expect(await badge.isHidden()).toBeFalse();
    expect(await badge.getText()).toBe('2');
  });

  it('should project the sections into the menu panel', async () => {
    bootstrapMenu();
    const menu = await getHarness(MatMenuHarness);

    await menu.open();

    const section = document.querySelector('.mat-mdc-menu-panel .projected-section');
    expect(section?.textContent?.trim()).toBe('Section');
  });

  it('should emit when the menu opens and closes', async () => {
    const context = bootstrapMenu();
    const menu = await getHarness(MatMenuHarness);

    await menu.open();
    await menu.close();

    expect(context.events).toEqual(['opened', 'closed']);
  });
});
