// Copyright 2026 Google LLC
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
import {
  ChangeDetectionStrategy,
  Component,
  computed,
  inject,
  OnDestroy,
  OnInit,
  signal,
} from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { ActivatedRoute } from '@angular/router';
import { Subscription } from 'rxjs';

import { ClusterQueue } from 'xmanager_cloud/ui/app/service/resource_types';

import {
  AutoReloadService,
  ReloaderControl,
} from '../common/components/autoreloader/autoreloader_service';
import { SmartResourceFinderComponent } from './components/smart_resource_finder/smart_resource_finder';
import {
  DashboardTab,
  SubnavTabsComponent,
  TAB_SMART_FINDER,
} from './components/subnav_tabs/subnav_tabs';

/**
 * Resource Observability Dashboard (ROD) primary layout container component.
 * Thin container orchestrating tab selection and cluster queue state.
 */
@Component({
  selector: 'xmc-resource-dashboard',
  standalone: true,
  templateUrl: './resource_dashboard.ng.html',
  styleUrl: './resource_dashboard.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, SubnavTabsComponent, SmartResourceFinderComponent],
})
export class ResourceDashboard implements OnInit, OnDestroy {
  private readonly autoReloadService = inject(AutoReloadService);
  private readonly route = inject(ActivatedRoute);

  protected readonly activeTab = signal<DashboardTab>(TAB_SMART_FINDER);

  private readonly resolvedData = toSignal(this.route.data);
  protected readonly clusterQueues = computed<ClusterQueue[]>(
    () => this.resolvedData()?.['clusterQueues'] ?? [],
  );
  protected readonly selectedQueue = signal<string>('gpu-cluster-queue');

  private readonly reloader: ReloaderControl = this.autoReloadService.createReloader({
    emitOnStart: false,
  });
  private readonly subscriptions = new Subscription();

  ngOnInit() {
    this.subscriptions.add(
      this.route.queryParams.subscribe((params) => {
        const queue = params['queue'];
        if (queue) {
          this.selectedQueue.set(queue);
        }
      }),
    );

    this.subscriptions.add(
      this.reloader.reloader$.subscribe(() => {
        // Auto-reload hook for periodic refresh
      }),
    );
    this.reloader.start();
  }

  ngOnDestroy() {
    this.subscriptions.unsubscribe();
    this.reloader.stop();
  }

  protected onQueueSelect(queueName: string) {
    this.selectedQueue.set(queueName);
  }
}
