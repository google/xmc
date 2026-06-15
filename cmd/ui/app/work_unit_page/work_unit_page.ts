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
import { ChangeDetectionStrategy, Component, computed, inject, Signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatIconModule } from '@angular/material/icon';
import { MatTabsModule } from '@angular/material/tabs';
import { MatTooltipModule } from '@angular/material/tooltip';
import { ActivatedRoute } from '@angular/router';
import { Experiment } from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import { StructuredMessage } from 'xmanager_cloud/experiment_state_server/proto/structured_message.jsonpb_decls';
import { WorkUnit } from 'xmanager_cloud/experiment_state_server/proto/work_unit.jsonpb_decls';
import { ArtifactsList } from 'xmanager_cloud/ui/app/common/components/artifacts_list/artifacts_list';
import { StatusMessagesList } from 'xmanager_cloud/ui/app/common/components/status_messages_list/status_messages_list';
import { FeatureFlagService } from 'xmanager_cloud/ui/app/service/feature_flag_service';

import {
  getStatusMessages,
  StatusMessage,
} from '../common/components/status_message/status_message';
import { UrlsWidget } from '../common/components/urls_widget/urls_widget';
import { ProcessedUrl, processUrls } from '../common/url/url_processor';

import { WorkUnitDetails } from './details/work_unit_details';
import { Header } from './header/header';
import { JobsTable } from './jobs_table/jobs_table';
import { WorkUnitPageStore } from './work_unit_page_store';

/**
 * Display details for a single work unit.
 */
@Component({
  selector: 'xmc-work-unit-page',
  standalone: true,
  templateUrl: './work_unit_page.ng.html',
  styleUrl: './work_unit_page.css',
  imports: [
    CommonModule,
    WorkUnitDetails,
    Header,
    JobsTable,
    MatCardModule,
    MatTabsModule,
    MatButtonModule,
    MatIconModule,
    MatTooltipModule,
    StatusMessage,
    UrlsWidget,
    ArtifactsList,
    StatusMessagesList,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  providers: [WorkUnitPageStore],
})
export class WorkUnitPage {
  private readonly store = inject(WorkUnitPageStore);
  private readonly route = inject(ActivatedRoute);
  private readonly featureFlagService = inject(FeatureFlagService);

  workUnit: Signal<WorkUnit | null> = this.store.workUnit;
  experiment: Signal<Experiment | null> = this.store.experiment;
  urls: Signal<ProcessedUrl[]> = computed(() => {
    const wu = this.workUnit();
    return wu ? processUrls(wu.urls ?? [], wu) : [];
  });
  statusMessages: Signal<StructuredMessage[]> = computed(() => {
    return getStatusMessages(this.workUnit()?.currentWorkUnitExecution?.status);
  });

  get isStatusMessageHistoryEnabled(): boolean {
    return this.featureFlagService.isStatusMessageHistoryEnabled;
  }

  isLoading: Signal<boolean> = this.store.isLoading;

  // The XID (experiment id) and WID (work unit id) of the work unit.
  xid: Signal<string | null> = this.store.xid;
  wid: Signal<string | null> = this.store.wid;

  ngOnInit() {
    this.store.setIds({
      xid: this.route.snapshot.paramMap.get('xid')!,
      wid: this.route.snapshot.paramMap.get('wid')!,
    });
    this.fetchWorkUnitPageData();
  }

  fetchWorkUnitPageData() {
    this.store.fetchWorkUnitPageData();
  }
}
