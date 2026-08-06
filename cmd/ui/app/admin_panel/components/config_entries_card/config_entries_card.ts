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

import { ChangeDetectionStrategy, Component, computed, input, signal } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';

export interface ConfigInfo {
  key?: string;
  value?: string;
}

export interface ConfigGroup {
  displayName?: string;
  configs?: ConfigInfo[];
}

/**
 * A component that displays configuration groups and key-value entries with client-side filtering.
 */
@Component({
  selector: 'xmc-config-entries-card',
  standalone: true,
  imports: [MatIconModule],
  templateUrl: './config_entries_card.ng.html',
  styleUrl: './config_entries_card.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ConfigEntriesCardComponent {
  configGroups = input<ConfigGroup[] | undefined>();

  protected readonly searchTerm = signal<string>('');

  protected readonly filteredGroups = computed(() => {
    const term = this.searchTerm().trim().toLowerCase();
    const groups = this.configGroups();
    if (!groups) return [];
    if (!term) return groups;

    return groups
      .map((group) => {
        const filteredConfigs = (group.configs || []).filter(
          (c) => c.key?.toLowerCase().includes(term) || c.value?.toLowerCase().includes(term),
        );
        return {
          ...group,
          configs: filteredConfigs,
        };
      })
      .filter((group) => group.configs.length > 0);
  });

  protected onSearchInput(event: Event): void {
    const inputElement = event.target as HTMLInputElement;
    this.searchTerm.set(inputElement.value);
  }
}
