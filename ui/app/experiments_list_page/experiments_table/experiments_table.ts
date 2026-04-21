import {ClipboardModule} from '@angular/cdk/clipboard';
import {CommonModule, DatePipe} from '@angular/common';
import {ChangeDetectionStrategy, Component, computed, EventEmitter, inject, input, Output} from '@angular/core';
import {toObservable} from '@angular/core/rxjs-interop';
import {MatButtonModule} from '@angular/material/button';
import {MatChipsModule} from '@angular/material/chips';
import {MatIconModule} from '@angular/material/icon';
import {MatProgressBarModule} from '@angular/material/progress-bar';
import {MatProgressSpinnerModule} from '@angular/material/progress-spinner';
import {MatSortModule, Sort} from '@angular/material/sort';
import {MatTableModule} from '@angular/material/table';
import {MatTooltipModule} from '@angular/material/tooltip';
import {RouterLink} from '@angular/router';
import * as api from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {Experiment} from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import {Priority} from 'xmanager_cloud/experiment_state_server/proto/priority.jsonpb_decls';
import {ExperimentStatus} from 'xmanager_cloud/experiment_state_server/proto/status.jsonpb_decls';
import {StructuredMessage} from 'xmanager_cloud/experiment_state_server/proto/structured_message.jsonpb_decls';
import {combineLatest, Observable} from 'rxjs';
import {map} from 'rxjs/operators';

import {LoadingBar} from '../../common/components/loading_bar/loading_bar';
import {PriorityChip} from '../../common/components/priority_chip/priority_chip';
import {ResourceChip} from '../../common/components/resource_chip/resource_chip';
import {ResourceChipData, resourceSetsToResourceChipsData} from '../../common/components/resource_chip/utils/resource_set_to_resource_chips';
import {StatusChip, StatusOption} from '../../common/components/status_chip/status_chip';
import {StatusMessage} from '../../common/components/status_message/status_message';
import {StatusMessageFilter} from '../../common/components/status_message_filter/status_message_filter';
import {Tag} from '../../common/components/tag/tag';
import {WorkUnitsStatusBar} from '../../common/components/work_units_status_bar/work_units_status_bar';
import {XidBadge} from '../../common/components/xid_badge/xid_badge';
import {StatusMessageFilterService} from '../../service/status_message_filter_service';
import {EditExperimentFormData} from '../../side_panel/contents/edit_experiment_form/edit_experiment_form';
import {SidePanelService} from '../../side_panel/side_panel_service';

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
  styleUrl: './experiments_table.css',
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
    StatusMessageFilter,
  ],
})
export class ExperimentsTable {
  readonly displayedColumns = Object.values(TableColumns);
  readonly TableColumns = TableColumns;

  readonly pageResult = input.required<api.ListExperimentsResponse|undefined>();
  readonly isFetching = input.required<boolean>();
  readonly pageSize = input<number>(10);

  readonly isLoading =
      computed(() => this.isFetching() && !this.pageResult());
  readonly isRefreshing =
      computed(() => this.isFetching() && !!this.pageResult());

  @Output() readonly sortChange = new EventEmitter<Sort>();

  private readonly sidePanelService = inject(SidePanelService);
  private readonly filterService = inject(StatusMessageFilterService);

  readonly tableRows$: Observable<TableRow[]>;

  constructor() {
    this.tableRows$ =
        combineLatest([
          toObservable(this.pageResult),
          this.filterService.severities$,
          this.filterService.sources$,
        ])
            .pipe(
                map(([pageResult, severities, sources]) => {
                  const experiments = pageResult?.experiments;
                  if (!experiments) {
                    return [];
                  }
                  return experiments.map((experiment: Experiment) => {
                    const message = !!experiment.status?.structuredStatusMessage
                                          ?.userFacingText ?
                        experiment.status?.structuredStatusMessage :
                        undefined;
                    let isStatusMessageVisible = false;
                    if (message) {
                      isStatusMessageVisible =
                          this.filterService.isMessageVisible(message);
                    }
                    return {
                      name: experiment.name,
                      xid: experiment.name?.split('/').pop(),
                      title: experiment.title,
                      tags: experiment.tags ?? [],
                      stateCounts: experiment.status?.childStateCounts ?? [],
                      status: getStatusChipEnum(experiment),
                      priority: experiment.priority,
                      notes: experiment.notes,
                      author: experiment.creator,
                      resources: experiment.resources ?
                          resourceSetsToResourceChipsData(
                              experiment.resources) :
                          [],
                      createTime: experiment.createTime ?
                          new Date(experiment.createTime) :
                          undefined,
                      completedTime: experiment.completeTime ?
                          new Date(experiment.completeTime) :
                          undefined,
                      structuredStatusMessage: message,
                      isStatusMessageVisible,
                    };
                  });
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
