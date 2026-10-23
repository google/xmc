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
import { ClipboardModule } from '@angular/cdk/clipboard';
import { CommonModule, DatePipe } from '@angular/common';
import { ChangeDetectionStrategy, Component, computed, inject, input, output } from '@angular/core';
import { toObservable } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatChipsModule } from '@angular/material/chips';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { MatSortModule, Sort } from '@angular/material/sort';
import { MatTableModule } from '@angular/material/table';
import { MatTooltipModule } from '@angular/material/tooltip';
import { RouterLink } from '@angular/router';
import { combineLatest, Observable } from 'rxjs';
import { map } from 'rxjs/operators';

import { FilterMenu } from 'app/common/components/filter_menu/filter_menu';
import { LoadingBar } from 'app/common/components/loading_bar/loading_bar';
import { PriorityChip } from 'app/common/components/priority_chip/priority_chip';
import { ResourceChip } from 'app/common/components/resource_chip/resource_chip';
import {
  ResourceChipData,
  resourceSetsToResourceChipsData,
} from 'app/common/components/resource_chip/resource_set_to_resource_chips';
import { StatusChip, StatusOption } from 'app/common/components/status_chip/status_chip';
import { StatusMessage } from 'app/common/components/status_message/status_message';
import { StatusMessageFilter } from 'app/common/components/status_message_filter/status_message_filter';
import { Tag } from 'app/common/components/tag/tag';
import { WorkUnitsStatusBar } from 'app/common/components/work_units_status_bar/work_units_status_bar';
import { XidBadge } from 'app/common/components/xid_badge/xid_badge';
import * as api from 'app/protos/experiment_state_server';
import {
  Experiment,
  ExperimentStatus,
  Priority,
  StructuredMessage,
} from 'app/protos/experiment_state_server';
import { StatusMessageFilterService } from 'app/service/status_message_filter_service';
import { EditExperimentFormData } from 'app/side_panel/contents/edit_experiment_form/edit_experiment_form';
import { SidePanelService } from 'app/side_panel/side_panel_service';

enum TableColumns {
  XID = 'XID',
  TITLE_AND_TAGS = 'Title and Tags',
  STATUS_AND_PRIORITY = 'Status and Priority',
  WORK_UNITS = 'Work Units',
  NOTES = 'Notes',
  RESOURCES = 'Resources',
  AUTHOR = 'Author',
  CREATED = 'Created',
  COMPLETED = 'Completed',
  ACTIONS = 'Actions',
}

interface TableRow {
  readonly name?: string;
  readonly xid?: string;
  readonly title?: string;
  readonly tags: string[];
  readonly status: StatusOption;
  readonly stateCounts: ExperimentStatus.StateCount[];
  readonly priority?: Priority;
  readonly notes?: string;
  readonly resources: ResourceChipData[];
  readonly author?: string;
  readonly createTime?: Date;
  readonly completedTime?: Date;
  readonly structuredStatusMessage?: StructuredMessage;
  readonly isStatusMessageVisible: boolean;
}

/**
 * Experiments table for the XMC UI.
 */
@Component({
  selector: 'xmc-experiments-table',
  standalone: true,
  templateUrl: './experiments_table.ng.html',
  styleUrl: './experiments_table.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    RouterLink,
    DatePipe,
    MatTableModule,
    MatIconModule,
    MatButtonModule,
    MatChipsModule,
    MatProgressSpinnerModule,
    MatSortModule,
    MatTooltipModule,
    WorkUnitsStatusBar,
    ClipboardModule,
    MatProgressBarModule,
    LoadingBar,
    Tag,
    XidBadge,
    StatusChip,
    ResourceChip,
    PriorityChip,
    StatusMessage,
    FilterMenu,
    StatusMessageFilter,
  ],
})
export class ExperimentsTable {
  readonly displayedColumns = Object.values(TableColumns);
  readonly TableColumns = TableColumns;

  readonly pageResult = input.required<api.ListExperimentsResponse | undefined>();
  readonly isFetching = input.required<boolean>();
  readonly pageSize = input<number>(10);

  readonly isLoading = computed(() => this.isFetching() && !this.pageResult());
  readonly isRefreshing = computed(() => this.isFetching() && !!this.pageResult());

  readonly sortChange = output<Sort>();

  private readonly sidePanelService = inject(SidePanelService);
  private readonly filterService = inject(StatusMessageFilterService);

  readonly tableRows$: Observable<TableRow[]>;

  constructor() {
    this.tableRows$ = combineLatest([
      toObservable(this.pageResult),
      this.filterService.severities$,
      this.filterService.sources$,
    ]).pipe(
      map(([pageResult, _severities, _sources]) => {
        const experiments = pageResult?.experiments;
        if (!experiments) {
          return [];
        }
        return experiments.map((experiment: Experiment) =>
          toTableRow(experiment, this.filterService),
        );
      }),
    );
  }

  editExperiment(xid: string) {
    this.sidePanelService.openSidePanel(new EditExperimentFormData(xid));
  }
}

function getStatusChipEnum(experiment: Experiment): StatusOption {
  if (experiment?.status?.activeState === 'ACTIVE') {
    return StatusOption.RUNNING;
  } else {
    return StatusOption.NOT_RUNNING;
  }
}

/**
 * Maps an Experiment to a TableRow.
 * Consolidated to maintain a clean linear mapper.
 */
function toTableRow(experiment: Experiment, filterService: StatusMessageFilterService): TableRow {
  const message = experiment.status?.structuredStatusMessage?.userFacingText
    ? experiment.status.structuredStatusMessage
    : undefined;
  const isVisible = !!message && filterService.isMessageVisible(message);

  return {
    name: experiment.name,
    xid: experiment.name?.split('/').at(-1),
    title: experiment.title,
    tags: experiment.tags || [],
    stateCounts: experiment.status?.childStateCounts || [],
    status: getStatusChipEnum(experiment),
    priority: experiment.priority,
    notes: experiment.notes,
    author: experiment.creator,
    resources: resourceSetsToResourceChipsData(experiment.resources || []),
    createTime: parseDate(experiment.createTime),
    completedTime: parseDate(experiment.completeTime),
    structuredStatusMessage: message,
    isStatusMessageVisible: isVisible,
  };
}

function parseDate(dateStr: string | undefined): Date | undefined {
  return dateStr ? new Date(dateStr) : undefined;
}
