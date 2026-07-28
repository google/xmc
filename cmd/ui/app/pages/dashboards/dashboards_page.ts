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
import { Component, inject, OnDestroy, OnInit, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { ActivatedRoute } from '@angular/router';
import { Subscription } from 'rxjs';

import {
  AutoReloadService,
  ReloaderControl,
} from 'xmanager_cloud/ui/app/common/components/autoreloader/autoreloader_service';
import {
  AddDashboardDialogResult,
  XmcAddDashboardDialogComponent,
} from 'xmanager_cloud/ui/app/common/components/dashboard/add_dashboard_dialog/add_dashboard_dialog';
import { XmcDashboardGridComponent } from 'xmanager_cloud/ui/app/common/components/dashboard/dashboard_grid/dashboard_grid';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';
import { Dashboard, LayoutType, PlotType } from 'xmanager_cloud/ui/app/service/dashboard_types';

/**
 * Root Dashboards page (/dashboards) displaying performance dashboards and comparative views.
 */
@Component({
  selector: 'xmc-dashboards-page',
  standalone: true,
  imports: [MatButtonModule, MatIconModule, MatProgressSpinnerModule, XmcDashboardGridComponent],
  templateUrl: './dashboards_page.ng.html',
  styleUrl: './dashboards_page.scss',
})
export class XmcDashboardsPageComponent implements OnInit, OnDestroy {
  readonly dashboards = signal<readonly Dashboard[]>([]);
  readonly selectedDashboard = signal<Dashboard | undefined>(undefined);
  readonly loading = signal<boolean>(true);
  readonly autoRefreshEnabled = signal<boolean>(true);

  private readonly dashboardService = inject(DashboardService);
  private readonly autoReloadService = inject(AutoReloadService);
  private readonly route = inject(ActivatedRoute);
  private readonly dialog = inject(MatDialog);

  private reloader?: ReloaderControl;
  private reloaderSub?: Subscription;

  ngOnInit(): void {
    const xidParam = this.route.snapshot.queryParamMap.get('xid');
    if (xidParam) {
      const xid = parseInt(xidParam, 10);
      if (!isNaN(xid) && xid > 0) {
        this.createComparativeDashboard({
          title: `Experiment ${xid} Performance`,
          xids: [xid],
        });
      }
    }

    this.reloader = this.autoReloadService.createReloader({ emitOnStart: true });
    this.reloaderSub = this.reloader.reloader$.subscribe(() => {
      this.loadDashboards();
    });
    this.reloader.start();
  }

  ngOnDestroy(): void {
    this.reloaderSub?.unsubscribe();
    this.reloader?.stop();
  }

  /** Toggles automatic background polling for live training metrics. */
  toggleAutoRefresh(): void {
    const next = !this.autoRefreshEnabled();
    this.autoRefreshEnabled.set(next);
    if (next) {
      this.reloader?.start();
    } else {
      this.reloader?.stop();
    }
  }

  /** Opens popup modal prompting for experiment XIDs to compare. */
  openAddDashboardPopup(): void {
    const dialogRef = this.dialog.open(XmcAddDashboardDialogComponent, {
      width: '460px',
    });

    dialogRef.afterClosed().subscribe((result: AddDashboardDialogResult | undefined) => {
      if (result) {
        this.createComparativeDashboard(result);
      }
    });
  }

  /** Selects active dashboard view. */
  selectDashboard(dashboard: Dashboard): void {
    this.selectedDashboard.set(dashboard);
  }

  private loadDashboards(): void {
    this.dashboardService.listDashboards().subscribe({
      next: (response) => {
        this.dashboards.set(response.dashboards);
        if (!this.selectedDashboard() && response.dashboards.length > 0) {
          this.selectedDashboard.set(response.dashboards[0]);
        }
        this.loading.set(false);
      },
      error: () => {
        this.loading.set(false);
      },
    });
  }

  private createComparativeDashboard(result: AddDashboardDialogResult): void {
    const charts = result.xids.map((xid) => ({
      name: `experiments/${xid}/charts/loss`,
      title: `Experiment ${xid} Performance`,
      xid,
      plots: [
        {
          plotId: `plot_summary_${xid}`,
          title: 'Loss Summary',
          type: PlotType.SUMMARY_BOX,
          data: { metrics: ['loss/train'], sources: [{ xid }] },
        },
        {
          plotId: `plot_curve_${xid}`,
          title: 'Training Loss',
          type: PlotType.SCATTER,
          data: { metrics: ['loss/train', 'loss/val'], sources: [{ xid }] },
        },
      ],
      plotLayout: { type: LayoutType.GRID },
    }));

    const dashboard: Dashboard = {
      name: `dashboards/custom-${Date.now()}`,
      title: result.title,
      charts,
      chartLayout: { type: LayoutType.SIDE_BY_SIDE },
    };

    this.dashboardService.createDashboard(dashboard).subscribe((created) => {
      this.dashboards.set([...this.dashboards(), created]);
      this.selectedDashboard.set(created);
    });
  }
}
