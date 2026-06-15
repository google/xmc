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
      const workUnit = this.workUnit();
      const jobs =
        workUnit?.currentWorkUnitExecution?.status?.detailedStatus?.jobsetStatus
          ?.replicatedJobsStatus || [];
      this.dataSource.data = jobs;
    });
  }

  getReplicaDisplays(job: ReplicatedJobStatus): ReplicaStatusDisplay[] {
    const displays: ReplicaStatusDisplay[] = [];
    const succeeded = Number(job.succeeded ?? 0);
    if (succeeded > 0) {
      displays.push({ status: StatusOption.SUCCESS, replicas: succeeded });
    }
    const failed = Number(job.failed ?? 0);
    if (failed > 0) {
      displays.push({ status: StatusOption.FAILED, replicas: failed });
    }

    // Running replicas are both active and ready, while pending replicas are
    // active but not yet ready.
    // E.G. if we have 10 replicas running and 2 are pending, we would
    // expect to see 12 replicas active and 10 replicas ready.
    const active = Number(job.active ?? 0);
    const ready = Number(job.ready ?? 0);
    const pending = Math.max(active - ready, 0);

    if (ready > 0) {
      displays.push({ status: StatusOption.RUNNING, replicas: ready });
    }
    if (pending > 0) {
      displays.push({ status: StatusOption.PENDING, replicas: pending });
    }
    return displays;
  }
}
