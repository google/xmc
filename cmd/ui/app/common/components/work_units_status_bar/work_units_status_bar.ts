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
import { ChangeDetectionStrategy, Component, computed, input, Signal } from '@angular/core';
import { MatMenuModule } from '@angular/material/menu';

import { ExperimentStatus, WorkUnitStatus } from 'app/protos/experiment_state_server';

interface StateCountView {
  readonly key: string;
  readonly label: string;
  readonly count: string;
  readonly width: string;
  readonly segmentClass: string;
  readonly squareClass: string;
}

const FALLBACK_STATE_CLASS = 'unknown-state';
const UNSPECIFIED_STATE = 'WORK_UNIT_STATE_UNSPECIFIED';

function stateSortIndex(state: WorkUnitStatus.WorkUnitState | undefined): number {
  const index = ORDERED_STATES.indexOf(state ?? UNSPECIFIED_STATE);
  return index === -1 ? ORDERED_STATES.length : index;
}

const STATE_CLASS_MAP: Readonly<Record<string, string>> = {
  INITIALIZING: 'INITIALIZING',
  PENDING: 'PENDING',
  SCHEDULING_PAUSED: 'SCHEDULING_PAUSED',
  SCHEDULING: 'SCHEDULING',
  STARTING: 'STARTING',
  RUNNING: 'RUNNING',
  PARTIALLY_FAILED: 'PARTIALLY_FAILED',
  STOPPING: 'STOPPING',
  RESTARTING: 'RESTARTING',
  STOPPED: 'STOPPED',
  DESCHEDULING: 'DESCHEDULING',
  COMPLETED: 'COMPLETED',
  FAILED: 'FAILED',
  WORK_UNIT_STATE_UNSPECIFIED: UNSPECIFIED_STATE,
};

/**
 * A horizontal stacked bar.
 * Showing the proportional distribution of an experiment's work units across
 * various states.
 */
@Component({
  selector: 'xmc-work-units-status-bar',
  standalone: true,
  templateUrl: './work_units_status_bar.ng.html',
  styleUrl: './work_units_status_bar.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, MatMenuModule],
})
export class WorkUnitsStatusBar {
  readonly stateCounts = input.required<ExperimentStatus.StateCount[]>();

  readonly workUnitsCount: Signal<number> = computed(() => {
    return this.stateCounts().reduce(
      (total, stateCount) => total + Number(stateCount?.count ?? 0),
      0,
    );
  });

  /** Sort by state so the status bar looks consistent. */
  readonly sortedStateCounts: Signal<ExperimentStatus.StateCount[]> = computed(() => {
    return this.stateCounts().toSorted((a, b) => stateSortIndex(a.state) - stateSortIndex(b.state));
  });

  protected readonly visibleStateViews: Signal<ReadonlyArray<StateCountView>> = computed(() => {
    return this.sortedStateCounts()
      .filter((stateCount) => Boolean(stateCount.count && stateCount.count !== '0'))
      .map((stateCount, index) => {
        const count = String(stateCount.count);
        const stateName = stateCount.state ?? UNSPECIFIED_STATE;
        const cssClass = stateCount.state
          ? (STATE_CLASS_MAP[stateCount.state] ?? FALLBACK_STATE_CLASS)
          : FALLBACK_STATE_CLASS;
        return {
          key: `${stateCount.state ?? UNSPECIFIED_STATE}-${index}`,
          label: stateName.toLowerCase(),
          count,
          width: this.getWidth(count),
          segmentClass: `state ${cssClass}`,
          squareClass: `square ${cssClass}`,
        };
      });
  });

  /**
   * Returns the width of the state segment as a percentage of the total work
   * units count. The value will be assigned to the `width` attribute.
   */
  getWidth(count: string): string {
    if (this.workUnitsCount() === 0) {
      return '0%';
    }

    const countInNumber = Number(count);
    const widthPercentage = (100 * countInNumber) / this.workUnitsCount();
    return `${widthPercentage}%`;
  }
}

/**
 * The order in which the states should be displayed in the status bar.
 */
const ORDERED_STATES: WorkUnitStatus.WorkUnitState[] = [
  'WORK_UNIT_STATE_UNSPECIFIED',
  'INITIALIZING',
  'SCHEDULING_PAUSED',
  'SCHEDULING',
  'STARTING',
  'RUNNING',
  'PARTIALLY_FAILED',
  'STOPPING',
  'RESTARTING',
  'STOPPED',
  'DESCHEDULING',
  'COMPLETED',
  'FAILED',
];
