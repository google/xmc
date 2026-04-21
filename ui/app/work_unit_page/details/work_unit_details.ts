import {CommonModule} from '@angular/common';
import {ChangeDetectionStrategy, Component, computed, input} from '@angular/core';
import {MatCardModule} from '@angular/material/card';
import {MatChipsModule} from '@angular/material/chips';
import {MatDividerModule} from '@angular/material/divider';
import {RouterModule} from '@angular/router';
import {type Experiment} from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import {type WorkUnit} from 'xmanager_cloud/experiment_state_server/proto/work_unit.jsonpb_decls';

import {Tag} from '../../common/components/tag/tag';

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
