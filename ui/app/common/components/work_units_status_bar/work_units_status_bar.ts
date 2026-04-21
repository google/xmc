import {CommonModule} from '@angular/common';
import {ChangeDetectionStrategy, Component, computed, input, Signal} from '@angular/core';
import {MatMenuModule} from '@angular/material/menu';
import {ExperimentStatus, WorkUnitStatus} from 'xmanager_cloud/experiment_state_server/proto/status.jsonpb_decls';

/**
 * A horizontal stacked bar.
 * Showing the proportional distribution of an experiment's work units across
 * various states.
 */
@Component({
  selector: 'xmc-work-units-status-bar',
  standalone: true,
  templateUrl: './work_units_status_bar.ng.html',
  styleUrl: './work_units_status_bar.css',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, MatMenuModule],
})
export class WorkUnitsStatusBar {
  readonly stateCounts = input.required<ExperimentStatus.StateCount[]>();

  readonly workUnitsCount: Signal<number> = computed(() => {
    return this.stateCounts().reduce(
        (total, stateCount) => total + Number(stateCount?.count ?? 0), 0);
  });

  /** Sort by state so the status bar looks consistent. */
  readonly sortedStateCounts: Signal<ExperimentStatus.StateCount[]> =
      computed(() => {
        return this.stateCounts().sort((a, b) => {
          return ORDERED_STATES.indexOf(
                     a.state ?? 'WORK_UNIT_STATE_UNSPECIFIED') -
              ORDERED_STATES.indexOf(b.state ?? 'WORK_UNIT_STATE_UNSPECIFIED');
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
