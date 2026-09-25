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
import { ChangeDetectionStrategy, Component, input } from '@angular/core';
import { MatChipsModule } from '@angular/material/chips';

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
  SCHEDULING_PAUSED = 'Scheduling Paused',
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
  styleUrl: './status_chip.scss',
  imports: [MatChipsModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class StatusChip {
  readonly status = input.required<StatusOption>();

  // Optional number of replicas to display in the chip. Used for replicated
  // jobs in a work unit.
  readonly replicas = input<number | undefined>(undefined);

  readonly StatusOption = StatusOption;
}
