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
import { Component, inject, input, OnInit, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';

import { XidBadge } from 'xmanager_cloud/ui/app/common/components/xid_badge/xid_badge';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';
import {
  Chart,
  ChartData,
  GetChartDataRequest,
  PlotData,
} from 'xmanager_cloud/ui/app/service/dashboard_types';

import { XmcPlotCardComponent } from '../plot_card/plot_card';

/**
 * Experiment-scoped chart card that fetches GetChartData in parallel and renders child plots.
 */
@Component({
  selector: 'xmc-chart-card',
  standalone: true,
  imports: [
    MatCardModule,
    MatIconModule,
    MatButtonModule,
    MatProgressSpinnerModule,
    XidBadge,
    XmcPlotCardComponent,
  ],
  templateUrl: './chart_card.ng.html',
  styleUrl: './chart_card.scss',
})
export class XmcChartCardComponent implements OnInit {
  readonly chart = input.required<Chart>();

  protected readonly chartData = signal<ChartData | undefined>(undefined);
  protected readonly loading = signal<boolean>(true);
  protected readonly error = signal<string | undefined>(undefined);

  private readonly dashboardService = inject(DashboardService);

  ngOnInit(): void {
    this.fetchChartData();
  }

  /** Refreshes chart metrics from DashboardService. */
  refresh(): void {
    this.fetchChartData();
  }

  /** Finds matching PlotData for a specific plotId. */
  protected getPlotData(plotId: string): PlotData | undefined {
    return this.chartData()?.plotsData.find((p) => p.plotId === plotId);
  }

  private fetchChartData(): void {
    this.loading.set(true);
    this.error.set(undefined);
    const req: GetChartDataRequest = { name: this.chart().name };
    this.dashboardService.getChartData(req).subscribe({
      next: (data) => {
        this.chartData.set(data);
        this.loading.set(false);
      },
      error: (_err) => {
        this.error.set(`Failed to fetch analytics for experiment ${this.chart().xid}`);
        this.loading.set(false);
      },
    });
  }
}
