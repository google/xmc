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
import { ChangeDetectionStrategy, Component, inject, OnInit, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatIconModule } from '@angular/material/icon';
import { RouterModule } from '@angular/router';

import { QueueJobsSidePanelData } from '../side_panel/contents/queue_jobs_side_panel/queue_jobs_side_panel';
import { SidePanelService } from '../side_panel/side_panel_service';

/**
 * Resource Observability Dashboard (ROD) scaffolding shell component.
 */
@Component({
  selector: 'xmc-resource-dashboard',
  standalone: true,
  templateUrl: './resource_dashboard.ng.html',
  styleUrl: './resource_dashboard.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, RouterModule, MatCardModule, MatIconModule, MatButtonModule],
})
export class ResourceDashboard implements OnInit {
  private readonly sidePanelService = inject(SidePanelService);
  protected readonly activeTab = signal<'queues' | 'predictability'>('queues');

  ngOnInit() {
    // TODO(#186): Auto-open side panel for PR 3 UI verification screenshot.
    this.sidePanelService.openSidePanel(new QueueJobsSidePanelData('cpu-cluster-queue'));
  }
}
