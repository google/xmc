// Copyright 2025 Google LLC
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
import { CommonModule } from '@angular/common';
import {
  ChangeDetectionStrategy,
  Component,
  computed,
  inject,
  input,
  output,
  signal,
} from '@angular/core';
import { MatBadgeModule } from '@angular/material/badge';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatTooltipModule } from '@angular/material/tooltip';

import { StructuredMessage } from 'app/protos/experiment_state_server';
import {
  ALL_SEVERITIES,
  ALL_SOURCES,
  formatSource,
  StatusMessageFilterService,
} from 'app/service/status_message_filter_service';

/** One selectable option (value sent to the parent, label shown) in the optional Status block. */
export interface StateFilterOption {
  readonly value: string;
  readonly label: string;
}

/**
 * Filters menu for status messages.
 *
 * By default it only filters which status messages are displayed. When the parent sets
 * `stateOptions`, the menu also shows a Status block above the status message filters; its
 * selection is emitted once, when the menu closes, so several states can be picked with a single
 * request. The parent owns what the selection filters.
 */
@Component({
  selector: 'xmc-status-message-filter',
  standalone: true,
  templateUrl: './status_message_filter.ng.html',
  styleUrl: './status_message_filter.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    MatBadgeModule,
    MatButtonModule,
    MatIconModule,
    MatMenuModule,
    MatTooltipModule,
    MatCardModule,
    MatCheckboxModule,
  ],
})
export class StatusMessageFilter {
  protected readonly filterService = inject(StatusMessageFilterService);
  protected readonly formatSource = formatSource;

  protected readonly allSeverities = ALL_SEVERITIES;
  protected readonly allSources = ALL_SOURCES;

  /** Options of the Status block; the block is hidden when undefined. */
  readonly stateOptions = input<readonly StateFilterOption[]>();
  /** Applied state selection; empty means no status filter. */
  readonly selectedStates = input<readonly string[]>([]);
  /** Emits the new selection, in option order, when the menu closes with a changed selection. */
  readonly selectedStatesChange = output<readonly string[]>();

  /** Selection being edited while the menu is open. */
  private readonly pendingStates = signal<ReadonlySet<string>>(new Set());

  protected readonly activeStateCount = computed(() => this.selectedStates().length);
  /** Screen-reader text for the badge, which MatBadge otherwise hides from assistive tech. */
  protected readonly badgeDescription = computed(() => {
    const count = this.activeStateCount();
    return count === 1 ? '1 status filter applied' : `${count} status filters applied`;
  });
  protected readonly triggerLabel = computed(() =>
    this.stateOptions() ? 'Filters' : 'Filter status messages',
  );
  /** Rows of the two-column Status grid, so options fill the first column top to bottom. */
  protected readonly stateGridRows = computed(
    () => `repeat(${Math.ceil((this.stateOptions()?.length ?? 0) / 2)}, auto)`,
  );

  protected isStatePending(value: string): boolean {
    return this.pendingStates().has(value);
  }

  protected onMenuOpened() {
    this.pendingStates.set(new Set(this.selectedStates()));
  }

  protected toggleState(value: string, isChecked: boolean) {
    const next = new Set(this.pendingStates());
    if (isChecked) {
      next.add(value);
    } else {
      next.delete(value);
    }
    this.pendingStates.set(next);
  }

  protected onMenuClosed() {
    const options = this.stateOptions();
    if (!options) {
      return;
    }
    const pending = this.pendingStates();
    const next = options.map((option) => option.value).filter((value) => pending.has(value));
    if (haveSameStates(next, this.selectedStates())) {
      return;
    }
    this.selectedStatesChange.emit(next);
  }

  toggleSeverity(severity: StructuredMessage.Severity, isChecked: boolean) {
    const current = new Set(this.filterService.severitiesSubject.value);
    if (isChecked) {
      current.add(severity);
    } else {
      current.delete(severity);
    }
    this.filterService.updateSeverities(current);
  }

  toggleSource(source: StructuredMessage.Source, isChecked: boolean) {
    const current = new Set(this.filterService.sourcesSubject.value);
    if (isChecked) {
      current.add(source);
    } else {
      current.delete(source);
    }
    this.filterService.updateSources(current);
  }

  protected formatSeverity(severity: StructuredMessage.Severity): string {
    return severity.charAt(0) + severity.slice(1).toLowerCase();
  }
}

/** Returns true when both selections contain the same states, ignoring order. */
function haveSameStates(a: readonly string[], b: readonly string[]): boolean {
  if (a.length !== b.length) {
    return false;
  }
  const bSet = new Set(b);
  return a.every((state) => bSet.has(state));
}
