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

import 'jasmine';

import { Component, signal } from '@angular/core';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import {
  bootstrap,
  cleanState,
  flush,
  getHarness,
  setupModule,
} from 'xmanager_cloud/ui/app/testing/test';

import { ConfigEntriesCardComponent, ConfigGroup } from './config_entries_card';
import { ConfigEntriesCardHarness } from './testing/config_entries_card_harness';

@Component({
  template: `<xmc-config-entries-card [configGroups]="groups()"></xmc-config-entries-card>`,
  standalone: true,
  imports: [ConfigEntriesCardComponent],
})
class TestHostComponent {
  readonly groups = signal<ConfigGroup[] | undefined>(undefined);
}

describe('ConfigEntriesCardComponent', () => {
  const mockGroups: ConfigGroup[] = [
    {
      displayName: 'Email Settings',
      configs: [
        { key: 'email.sender', value: 'admin@google.com' },
        { key: 'email.recipient', value: 'alerts@google.com' },
      ],
    },
    {
      displayName: 'General Settings',
      configs: [
        { key: 'port', value: '8080' },
        { key: 'projectId', value: 'my-project-123' },
      ],
    },
  ];

  cleanState(() => {
    setupModule({
      imports: [TestHostComponent, NoopAnimationsModule],
    });
  }, beforeEach);

  it('should display config groups and key-value entries', async () => {
    const host = bootstrap(TestHostComponent);
    host.groups.set(mockGroups);
    await flush();

    const harness = await getHarness(ConfigEntriesCardHarness);
    const names = await harness.getGroupDisplayNames();
    const entries = await harness.getConfigEntries();

    expect(names).toEqual(['Email Settings', 'General Settings']);
    expect(entries.length).toBe(4);
    expect(entries[0]).toEqual({ key: 'email.sender', value: 'admin@google.com' });
    expect(entries[2]).toEqual({ key: 'port', value: '8080' });
  });

  it('should filter entries by key', async () => {
    const host = bootstrap(TestHostComponent);
    host.groups.set(mockGroups);
    await flush();

    const harness = await getHarness(ConfigEntriesCardHarness);
    await harness.enterSearchTerm('port');
    await flush();

    // Only General Settings group should be shown, with the port config
    expect(await harness.getGroupDisplayNames()).toEqual(['General Settings']);
    const entries = await harness.getConfigEntries();
    expect(entries.length).toBe(1);
    expect(entries[0]).toEqual({ key: 'port', value: '8080' });
  });

  it('should filter entries by value', async () => {
    const host = bootstrap(TestHostComponent);
    host.groups.set(mockGroups);
    await flush();

    const harness = await getHarness(ConfigEntriesCardHarness);
    await harness.enterSearchTerm('alerts');
    await flush();

    expect(await harness.getGroupDisplayNames()).toEqual(['Email Settings']);
    const entries = await harness.getConfigEntries();
    expect(entries.length).toBe(1);
    expect(entries[0]).toEqual({ key: 'email.recipient', value: 'alerts@google.com' });
  });

  it('should be case-insensitive', async () => {
    const host = bootstrap(TestHostComponent);
    host.groups.set(mockGroups);
    await flush();

    const harness = await getHarness(ConfigEntriesCardHarness);
    await harness.enterSearchTerm('SENDER');
    await flush();

    expect(await harness.getGroupDisplayNames()).toEqual(['Email Settings']);
    const entries = await harness.getConfigEntries();
    expect(entries.length).toBe(1);
    expect(entries[0]).toEqual({ key: 'email.sender', value: 'admin@google.com' });
  });

  it('should display "No entries match the filter" when no matches are found', async () => {
    const host = bootstrap(TestHostComponent);
    host.groups.set(mockGroups);
    await flush();

    const harness = await getHarness(ConfigEntriesCardHarness);
    await harness.enterSearchTerm('nonexistent');
    await flush();

    expect(await harness.getGroupDisplayNames()).toEqual([]);
    expect(await harness.getConfigEntries()).toEqual([]);
    expect(await harness.getNoResultsText()).toContain('No entries match the filter');
  });

  it('should collapse and expand group entries when group header is clicked', async () => {
    const host = bootstrap(TestHostComponent);
    host.groups.set(mockGroups);
    await flush();

    const harness = await getHarness(ConfigEntriesCardHarness);
    await harness.toggleGroup(0);
    await flush();
    const isCollapsed = await harness.isGroupCollapsed(0);
    const collapsedEntries = await harness.getConfigEntries();

    expect(isCollapsed).toBeTrue();
    expect(collapsedEntries.length).toBe(2);
    expect(collapsedEntries[0]).toEqual({ key: 'port', value: '8080' });

    await harness.toggleGroup(0);
    await flush();
    const isExpanded = !(await harness.isGroupCollapsed(0));
    const expandedEntries = await harness.getConfigEntries();

    expect(isExpanded).toBeTrue();
    expect(expandedEntries.length).toBe(4);
  });

  it('should fallback to Configuration when group displayName is undefined or empty', async () => {
    const host = bootstrap(TestHostComponent);
    host.groups.set([
      {
        displayName: undefined,
        configs: [{ key: 'anonymous.key', value: 'anonymous.value' }],
      },
      {
        displayName: 'Named Group',
        configs: [{ key: 'named.key', value: 'named.value' }],
      },
    ]);
    await flush();
    const harness = await getHarness(ConfigEntriesCardHarness);
    await harness.toggleGroup(0);
    await flush();

    const names = await harness.getGroupDisplayNames();
    const isCollapsed = await harness.isGroupCollapsed(0);
    const entries = await harness.getConfigEntries();

    expect(names).toEqual(['Configuration', 'Named Group']);
    expect(isCollapsed).toBeTrue();
    expect(entries).toEqual([{ key: 'named.key', value: 'named.value' }]);
  });
});
