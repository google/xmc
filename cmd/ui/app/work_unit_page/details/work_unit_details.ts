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
import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';
import { MatCardModule } from '@angular/material/card';
import { MatChipsModule } from '@angular/material/chips';
import { MatDividerModule } from '@angular/material/divider';
import { RouterModule } from '@angular/router';

import { type Experiment } from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import { type WorkUnit } from 'xmanager_cloud/experiment_state_server/proto/work_unit.jsonpb_decls';
import { Tag } from 'xmanager_cloud/ui/app/common/components/tag/tag';

/**
 * Work unit details component.
 */
@Component({
  selector: 'xmc-work-unit-details',
  standalone: true,
  templateUrl: './work_unit_details.ng.html',
  styleUrl: './work_unit_details.scss',
  imports: [
    // go/keep-sorted start
    CommonModule,
    MatCardModule,
    MatChipsModule,
    MatDividerModule,
    RouterModule,
    Tag,
    // go/keep-sorted end
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class WorkUnitDetails {
  readonly workUnit = input.required<WorkUnit>();
  readonly experiment = input.required<Experiment>();

  readonly experimentId = computed(() => this.workUnit().name!.split('/')[1]);
  readonly workUnitId = computed(() => this.workUnit().name!.split('/').pop()!);
}
