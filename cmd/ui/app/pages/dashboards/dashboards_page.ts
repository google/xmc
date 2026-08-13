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

import { CommonModule } from '@angular/common';
import { ChangeDetectionStrategy, Component, inject, OnInit, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog, MatDialogModule } from '@angular/material/dialog';
import { MatIconModule } from '@angular/material/icon';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { ActivatedRoute } from '@angular/router';

import {
  AddExperimentDialogComponent,
  AddExperimentDialogResult,
} from 'xmanager_cloud/ui/app/common/components/dashboard/add_experiment_dialog/add_experiment_dialog';
import { DashboardGridComponent } from 'xmanager_cloud/ui/app/common/components/dashboard/dashboard_grid/dashboard_grid';
import {
  Chart,
  LayoutType,
  PlotSpec,
  PlotType,
} from 'xmanager_cloud/ui/app/service/dashboard_models';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';
import { SnackbarService } from 'xmanager_cloud/ui/app/service/snackbar_service';

function createDefaultPlots(): PlotSpec[] {
  return [
    {
      plotId: 'val_loss_kpi',
      title: 'Avg validation loss',
      type: PlotType.SUMMARY_BOX,
      data: { metrics: ['val/loss'] },
    },
    {
      plotId: 'train_acc_kpi',
      title: 'Avg training accuracy',
      type: PlotType.SUMMARY_BOX,
      data: { metrics: ['train/accuracy'] },
    },
    {
      plotId: 'loss_vs_steps',
      title: 'Validation loss vs steps',
      type: PlotType.SCATTER,
      data: { metrics: ['val/loss'] },
    },
    {
      plotId: 'acc_vs_steps',
      title: 'Training accuracy vs steps',
      type: PlotType.SCATTER,
      data: { metrics: ['train/accuracy'] },
    },
  ];
}

function createDefaultCharts(): Chart[] {
  const plots = createDefaultPlots();
  const experiments = [
    { xid: 234911122, name: 'baseline', title: 'VCC Baseline' },
    { xid: 234911123, name: 'tuned_v1', title: 'VCC tuned_v1' },
    { xid: 234911124, name: 'tuned_v2', title: 'VCC tuned_v2' },
  ];

  return experiments.map((exp) => ({
    name: `experiments/${exp.xid}/charts/${exp.name}`,
    title: exp.title,
    xid: exp.xid,
    wid: 1,
    plotLayout: { type: LayoutType.GRID },
    plots,
  }));
}

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
    MatButtonModule,
    MatIconModule,
    MatSlideToggleModule,
    MatDialogModule,
    DashboardGridComponent,
  ],
})
export class DashboardsPage implements OnInit {
  private readonly route = inject(ActivatedRoute);
  private readonly dashboardService = inject(DashboardService);
  private readonly dialog = inject(MatDialog);
  private readonly snackbar = inject(SnackbarService, { optional: true });

  readonly dashboardTitle = signal<string>('VCC baseline tune comparison');
  readonly autosaveEnabled = signal<boolean>(true);
  readonly charts = signal<Chart[]>([]);
  readonly isLoading = signal<boolean>(false);

  ngOnInit(): void {
    const dashboardId = this.route.snapshot.paramMap.get('id');
    if (dashboardId) {
      this.loadDashboard(`dashboards/${dashboardId}`);
    } else {
      this.initDefaultComparisonDashboard();
    }
  }

  loadDashboard(name: string): void {
    this.isLoading.set(true);
    this.dashboardService.getDashboard(name).subscribe({
      next: (dashboard) => {
        this.dashboardTitle.set(dashboard.title);
        this.charts.set(dashboard.charts ?? []);
        this.isLoading.set(false);
      },
      error: () => {
        this.initDefaultComparisonDashboard();
        this.isLoading.set(false);
      },
    });
  }

  private initDefaultComparisonDashboard(): void {
    this.charts.set(createDefaultCharts());
  }

  onRefresh(): void {
    // Re-trigger by updating the array reference
    this.charts.update((current) => [...current]);
    this.snackbar?.open?.('Dashboard refreshed');
  }

  onOpenAddExperiment(): void {
    const dialogRef = this.dialog.open(AddExperimentDialogComponent, {
      width: '440px',
    });

    dialogRef.afterClosed().subscribe((result?: AddExperimentDialogResult) => {
      if (!result) return;

      const templatePlots = this.charts()[0]?.plots ?? [];
      const newChart: Chart = {
        name: `experiments/${result.xid}/charts/custom_${Date.now()}`,
        title: result.title || `Experiment ${result.xid}`,
        xid: result.xid,
        wid: result.wid ?? 1,
        plotLayout: { type: LayoutType.GRID },
        plots: templatePlots,
      };

      this.charts.update((c) => [...c, newChart]);
      this.snackbar?.open?.(`Added experiment ${result.xid} to comparison`);
    });
  }

  onRemoveChart(chartName: string): void {
    this.charts.update((c) =>
      c.filter((chart) => chart.name !== chartName && `${chart.xid}` !== chartName),
    );
  }

  onShare(): void {
    if (navigator.clipboard) {
      navigator.clipboard.writeText(window.location.href);
      this.snackbar?.open?.('Dashboard link copied to clipboard');
    }
  }
}
