import {ChangeDetectionStrategy, Component, input} from '@angular/core';
import {MatChipsModule} from '@angular/material/chips';

/**
 * Input options for the status chip.
 */
export enum StatusOption {
  RUNNING = 'Running',
  STOPPED = 'Stopped',
  STOPPING = 'Stopping',
  STARTING = 'Starting',
  DESCHEDULING = 'Descheduling',
  SCHEDULING = 'Scheduling',
  NOT_RUNNING = 'Not Running',
  RESTARTING = 'Restarting',
  INITIALIZING = 'Initializing',
  COMPLETED = 'Completed',
  SUCCESS = 'Success',
  FAILED = 'Failed',
  PENDING = 'Pending',
  UNKNOWN = 'Unknown',
}

/**
 * Display a status chip for an experiment, work unit, or job.
 */
@Component({
  selector: 'xmc-status-chip',
  standalone: true,
  templateUrl: './status_chip.ng.html',
  styleUrl: './status_chip.css',
  imports: [MatChipsModule],
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class StatusChip {
  readonly status = input.required<StatusOption>();

  // Optional number of replicas to display in the chip. Used for replicated
  // jobs in a work unit.
  readonly replicas = input<number|undefined>(undefined);

  readonly StatusOption = StatusOption;
}
