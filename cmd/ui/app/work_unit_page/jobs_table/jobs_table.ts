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
import { ChangeDetectionStrategy, Component, effect, input, ViewChild } from '@angular/core';
import { MatCardModule } from '@angular/material/card';
import { MatPaginator, MatPaginatorModule } from '@angular/material/paginator';
import { MatTableDataSource, MatTableModule } from '@angular/material/table';

import { type ReplicatedJobStatus } from 'xmanager_cloud/experiment_state_server/proto/status.jsonpb_decls';
import { type WorkUnit } from 'xmanager_cloud/experiment_state_server/proto/work_unit.jsonpb_decls';
import {
  StatusChip,
  StatusOption,
} from 'xmanager_cloud/ui/app/common/components/status_chip/status_chip';

interface ReplicaStatusDisplay {
  status: StatusOption;
  replicas: number;
}

/** Work unit jobs table component. */
@Component({
  selector: 'xmc-work-unit-jobs-table',
  standalone: true,
  templateUrl: './jobs_table.ng.html',
  styleUrl: './jobs_table.scss',
  imports: [CommonModule, MatTableModule, MatPaginatorModule, MatCardModule, StatusChip],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class JobsTable {
  readonly workUnit = input.required<WorkUnit>();

  readonly StatusOption = StatusOption;

  dataSource = new MatTableDataSource<ReplicatedJobStatus>();
  @ViewChild(MatPaginator)
  set matPaginator(paginator: MatPaginator) {
    this.dataSource.paginator = paginator;
  }
  readonly displayedColumns = ['name', 'replicas'];

  constructor() {
    // Update the table data whenever the workUnit input changes.
    effect(() => {
      this.dataSource.data = getReplicatedJobs(this.workUnit());
    });
  }

  getReplicaDisplays(job: ReplicatedJobStatus): ReplicaStatusDisplay[] {
    const displays: ReplicaStatusDisplay[] = [];
    this.addDisplay(displays, StatusOption.SUCCESS, job.succeeded);
    this.addDisplay(displays, StatusOption.FAILED, job.failed);
    this.addDisplay(displays, StatusOption.RUNNING, job.ready);

    const active = Number(job.active || 0);
    const ready = Number(job.ready || 0);
    const pending = Math.max(active - ready, 0);

    if (pending > 0) {
      displays.push({ status: StatusOption.PENDING, replicas: pending });
    }
    return displays;
  }

  private addDisplay(
    displays: ReplicaStatusDisplay[],
    status: StatusOption,
    countVal: string | number | undefined,
  ) {
    const count = Number(countVal || 0);
    if (count > 0) {
      displays.push({ status, replicas: count });
    }
  }
}

function getReplicatedJobs(workUnit: WorkUnit): ReplicatedJobStatus[] {
  const jobset = getJobsetStatus(workUnit);
  return jobset?.replicatedJobsStatus || [];
}

function getJobsetStatus(workUnit: WorkUnit) {
  const execution = workUnit.currentWorkUnitExecution;
  if (!execution) {
    return null;
  }
  const status = execution.status;
  if (!status) {
    return null;
  }
  return status.detailedStatus?.jobsetStatus;
}
