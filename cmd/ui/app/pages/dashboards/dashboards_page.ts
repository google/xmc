// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import { Clipboard } from '@angular/cdk/clipboard';
import { CommonModule } from '@angular/common';
import { ChangeDetectionStrategy, Component, inject, OnInit, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog, MatDialogModule } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { ActivatedRoute, Router, RouterLink } from '@angular/router';
import { map, Observable } from 'rxjs';

import {
  AddExperimentDialogComponent,
  AddExperimentDialogResult,
} from 'xmanager_cloud/ui/app/common/components/dashboard/add_experiment_dialog/add_experiment_dialog';
import { DashboardGridComponent } from 'xmanager_cloud/ui/app/common/components/dashboard/dashboard_grid/dashboard_grid';
import { Chart, LayoutType } from 'xmanager_cloud/ui/app/service/dashboard_models';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';
import { SnackbarService } from 'xmanager_cloud/ui/app/service/snackbar_service';

/**
 * Main Dashboards Page component rendering comparative metrics dashboards.
 */
@Component({
  selector: 'xmc-dashboards-page',
  standalone: true,
  templateUrl: './dashboards_page.ng.html',
  styleUrl: './dashboards_page.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    FormsModule,
    RouterLink,
    MatButtonModule,
    MatIconModule,
    MatSlideToggleModule,
    MatDialogModule,
    MatProgressBarModule,
    DashboardGridComponent,
  ],
})
export class DashboardsPage implements OnInit {
  private readonly route = inject(ActivatedRoute);
  private readonly router = inject(Router);
  private readonly dashboardService = inject(DashboardService);
  private readonly dialog = inject(MatDialog);
  private readonly snackbar = inject(SnackbarService, { optional: true });
  private readonly clipboard = inject(Clipboard);

  private currentDashboardName = '';

  readonly dashboardTitle = signal<string>('Dashboard');
  readonly autosaveEnabled = signal<boolean>(true);
  readonly charts = signal<Chart[]>([]);
  readonly isLoading = signal<boolean>(false);
  readonly dashboardNotFound = signal<boolean>(false);

  ngOnInit(): void {
    const dashboardId = this.route.snapshot.paramMap.get('id');
    if (dashboardId) {
      const resourceName = dashboardId.startsWith('dashboards/')
        ? dashboardId
        : `dashboards/${dashboardId}`;
      this.loadDashboard(resourceName);
    } else {
      this.dashboardNotFound.set(true);
    }
  }

  loadDashboard(name: string): void {
    this.currentDashboardName = name;
    this.isLoading.set(true);
    this.dashboardNotFound.set(false);
    this.dashboardService.getDashboard(name).subscribe({
      next: (dashboard) => {
        this.dashboardTitle.set(dashboard.title || dashboard.name || 'Dashboard');
        // The dashboard resource carries chart IDs only, so the chart bodies come from ListCharts.
        this.fetchCharts().subscribe({
          next: (charts) => {
            this.charts.set(charts);
            this.isLoading.set(false);
          },
          error: () => {
            this.charts.set([]);
            this.isLoading.set(false);
            this.snackbar?.open?.('Failed to load dashboard charts');
          },
        });
      },
      error: () => {
        this.dashboardNotFound.set(true);
        this.isLoading.set(false);
      },
    });
  }

  onRefresh(): void {
    if (!this.currentDashboardName) return;

    this.fetchCharts().subscribe({
      next: (charts) => {
        this.charts.set(charts);
        this.snackbar?.open?.('Dashboard refreshed');
      },
      error: () => this.snackbar?.open?.('Failed to refresh dashboard'),
    });
  }

  private fetchCharts(): Observable<Chart[]> {
    return this.dashboardService
      .listCharts({ parent: this.currentDashboardName })
      .pipe(map((response) => response.charts ?? []));
  }

  onOpenAddExperiment(): void {
    const dialogRef = this.dialog.open(AddExperimentDialogComponent, {
      width: '440px',
    });

    dialogRef.afterClosed().subscribe((result?: AddExperimentDialogResult) => {
      if (!result) return;

      const templatePlots = this.charts()[0]?.plots ? structuredClone(this.charts()[0].plots) : [];
      const newChart: Chart = {
        name: `experiments/${result.xid}/charts/custom_${Date.now()}`,
        title: result.title || `Experiment ${result.xid}`,
        xid: result.xid,
        wid: result.wid ?? 1,
        plotLayout: { type: LayoutType.GRID },
        plots: templatePlots,
      };

      this.charts.update((c) => [...c, newChart]);
      this.triggerAutosave();
      this.snackbar?.open?.(`Added experiment ${result.xid} to comparison`);
    });
  }

  onRemoveChart(chartName: string): void {
    this.charts.update((c) =>
      c.filter((chart) => chart.name !== chartName && `${chart.xid}` !== chartName),
    );
    this.triggerAutosave();
  }

  private triggerAutosave(): void {
    if (!this.autosaveEnabled() || !this.currentDashboardName) return;

    this.dashboardService
      .updateDashboard({
        dashboard: {
          name: this.currentDashboardName,
          title: this.dashboardTitle(),
          chartIds: this.charts()
            .map((chart) => chart.name?.split('/').pop() ?? '')
            .filter((chartId) => chartId !== ''),
        },
      })
      .subscribe({
        error: (err) => console.error('Failed to autosave dashboard:', err),
      });
  }

  onShare(): void {
    const success = this.clipboard.copy(window.location.href);
    if (!success) {
      this.snackbar?.open?.('Failed to copy link to clipboard');
      return;
    }
    this.snackbar?.open?.('Dashboard link copied to clipboard');
  }

  onBackToDashboards(): void {
    this.router.navigate(['/dashboards']);
  }
}
