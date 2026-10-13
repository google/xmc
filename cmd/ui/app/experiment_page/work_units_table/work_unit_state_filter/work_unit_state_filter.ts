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
import { ChangeDetectionStrategy, Component, inject, input, output, signal } from '@angular/core';
import { MatCheckboxModule } from '@angular/material/checkbox';

import { FilterMenu } from 'app/common/components/filter_menu/filter_menu';

/** One selectable work unit state: the value sent to the parent and the label shown. */
interface StateFilterOption {
  readonly value: string;
  readonly label: string;
}

/**
 * Status filter labels keyed by work unit state, in proto enum order (UNSPECIFIED excluded).
 * The spec asserts the full list, since the OSS proto stub types states as plain strings.
 */
const STATE_FILTER_LABELS: Readonly<Record<string, string>> = {
  INITIALIZING: 'Initializing',
  SCHEDULING: 'Scheduling',
  STARTING: 'Starting',
  RUNNING: 'Running',
  PARTIALLY_FAILED: 'Partially Failed',
  STOPPING: 'Stopping',
  RESTARTING: 'Restarting',
  STOPPED: 'Stopped',
  DESCHEDULING: 'Descheduling',
  COMPLETED: 'Completed',
  FAILED: 'Failed',
  SCHEDULING_PAUSED: 'Scheduling Paused',
};

/** Every work unit state the filter offers, in proto enum order. */
const STATE_FILTER_OPTIONS: readonly StateFilterOption[] = Object.entries(STATE_FILTER_LABELS).map(
  ([value, label]) => ({ value, label }),
);

/**
 * Status section of a filter menu: filters work units by state.
 *
 * Must be projected into an `xmc-filter-menu`. The selection is edited while the menu is open and
 * emitted once, when it closes, so several states can be picked with a single request. The parent
 * owns what the selection filters.
 */
@Component({
  selector: 'xmc-work-unit-state-filter',
  standalone: true,
  templateUrl: './work_unit_state_filter.ng.html',
  styleUrl: './work_unit_state_filter.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [MatCheckboxModule],
})
export class WorkUnitStateFilter {
  protected readonly options = STATE_FILTER_OPTIONS;
  /** Rows of the two-column grid, so options fill the first column top to bottom. */
  protected readonly gridRows = `repeat(${Math.ceil(STATE_FILTER_OPTIONS.length / 2)}, auto)`;

  /** Applied state selection; empty means no status filter. */
  readonly selectedStates = input<readonly string[]>([]);
  /** Emits the new selection, in option order, when the menu closes with a changed selection. */
  readonly selectedStatesChange = output<readonly string[]>();

  /** Selection being edited while the menu is open. */
  private readonly pendingStates = signal<ReadonlySet<string>>(new Set());
  /**
   * Applied selection when the menu opened. Closing diffs against it rather than the live input,
   * so an external change while the menu is open is not overwritten when nothing was edited.
   */
  private statesAtOpen: readonly string[] = [];

  constructor() {
    const menu = inject(FilterMenu);
    menu.opened.subscribe(() => this.onMenuOpened());
    menu.closed.subscribe(() => this.onMenuClosed());
  }

  protected isStatePending(value: string): boolean {
    return this.pendingStates().has(value);
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

  private onMenuOpened() {
    this.statesAtOpen = this.selectedStates();
    this.pendingStates.set(new Set(this.statesAtOpen));
  }

  private onMenuClosed() {
    const pending = this.pendingStates();
    const atOpen = this.statesAtOpen;
    if (pending.size === atOpen.length && atOpen.every((state) => pending.has(state))) {
      return;
    }
    this.selectedStatesChange.emit(
      this.options.map((option) => option.value).filter((value) => pending.has(value)),
    );
  }
}
