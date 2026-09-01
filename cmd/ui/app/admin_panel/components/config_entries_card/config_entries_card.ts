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

import { ChangeDetectionStrategy, Component, computed, input, output, signal } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';

import { ErrorBannerComponent } from 'xmanager_cloud/ui/app/common/components/error_banner/error_banner';

export interface ConfigInfo {
  key?: string;
  value?: string;
}

export interface ConfigGroup {
  displayName?: string;
  configs?: ConfigInfo[];
}

export interface ConfigGroupItem extends ConfigGroup {
  id: string;
  displayName: string;
  isCollapsed: boolean;
}

/**
 * A component that displays configuration groups and key-value entries with client-side filtering.
 */
@Component({
  selector: 'xmc-config-entries-card',
  standalone: true,
  imports: [MatIconModule, ErrorBannerComponent, MatProgressSpinnerModule],
  templateUrl: './config_entries_card.ng.html',
  styleUrl: './config_entries_card.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ConfigEntriesCardComponent {
  readonly configGroups = input<ConfigGroup[] | undefined>();
  readonly errorMessage = input<string | null>(null);
  readonly loading = input<boolean>(false);
  readonly retry = output<void>();

  protected readonly searchTerm = signal<string>('');
  protected readonly collapsedGroups = signal<Set<string>>(new Set());

  protected readonly filteredGroups = computed<ConfigGroupItem[]>(() =>
    this.filterConfigGroups(this.configGroups(), this.searchTerm(), this.collapsedGroups()),
  );

  protected onSearchInput(event: Event): void {
    const inputElement = event.target as HTMLInputElement;
    this.searchTerm.set(inputElement.value);
  }

  protected onRetry(): void {
    this.retry.emit();
  }

  protected toggleGroup(groupId?: string): void {
    if (!groupId) return;
    this.collapsedGroups.update((current) => {
      const next = new Set(current);
      if (next.has(groupId)) {
        next.delete(groupId);
      } else {
        next.add(groupId);
      }
      return next;
    });
  }

  private filterConfigGroups(
    groups: ConfigGroup[] | undefined,
    term: string,
    collapsed: Set<string>,
  ): ConfigGroupItem[] {
    if (!groups) return [];
    const searchTerm = term.trim().toLowerCase();

    return groups
      .map((group, index) => {
        const displayName = group.displayName || 'Configuration';
        const id = `config-group-${index}-${displayName}`;
        const filteredConfigs = (group.configs || []).filter(
          (c) =>
            !searchTerm ||
            c.key?.toLowerCase().includes(searchTerm) ||
            c.value?.toLowerCase().includes(searchTerm),
        );
        return {
          ...group,
          id,
          displayName,
          isCollapsed: collapsed.has(id),
          configs: filteredConfigs,
        };
      })
      .filter((group) => (group.configs || []).length > 0);
  }
}
