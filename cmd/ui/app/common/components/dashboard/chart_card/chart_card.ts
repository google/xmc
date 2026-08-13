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
import {
  ChangeDetectionStrategy,
  Component,
  computed,
  inject,
  input,
  OnInit,
  output,
  signal,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { RouterModule } from '@angular/router';

import {
  Chart,
  ChartData,
  PlotData,
  PlotSpec,
  PlotType,
} from 'xmanager_cloud/ui/app/service/dashboard_models';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';

import { PlotCardComponent } from '../plot_card/plot_card';

/**
 * Chart card component representing an experiment's visualization column.
 */
@Component({
  selector: 'xmc-chart-card',
  standalone: true,
  templateUrl: './chart_card.ng.html',
  styleUrl: './chart_card.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    RouterModule,
    MatCardModule,
    MatButtonModule,
    MatIconModule,
    MatMenuModule,
    MatProgressSpinnerModule,
    PlotCardComponent,
  ],
})
export class ChartCardComponent implements OnInit {
  readonly chart = input.required<Chart>();
  readonly isBaseline = input<boolean>(false);
  readonly remove = output<string>();

  private readonly dashboardService = inject(DashboardService);

  readonly chartData = signal<ChartData | undefined>(undefined);
  readonly isLoading = signal<boolean>(false);
  readonly error = signal<string | null>(null);

  protected readonly headerTitle = computed(() => {
    const c = this.chart();
    const xidStr = `XID/${c.xid}`;
    const widStr = c.wid !== undefined ? ` - WorkUnitID ${c.wid}` : '';
    const titleStr = c.title ? ` - ${c.title}` : '';
    return `${xidStr}${titleStr}${widStr}`;
  });

  protected readonly experimentUrl = computed(() => {
    const c = this.chart();
    if (c.wid !== undefined) {
      return `/experiments/${c.xid}/workUnits/${c.wid}`;
    }
    return `/experiments/${c.xid}`;
  });

  protected readonly summaryPlots = computed<PlotSpec[]>(() => {
    return this.chart().plots.filter((p) => p.type === PlotType.SUMMARY_BOX || p.type === 1);
  });

  protected readonly curvePlots = computed<PlotSpec[]>(() => {
    return this.chart().plots.filter((p) => p.type !== PlotType.SUMMARY_BOX && p.type !== 1);
  });

  ngOnInit(): void {
    this.fetchChartData();
  }

  fetchChartData(): void {
    const c = this.chart();
    const chartName = c.name || `experiments/${c.xid}/charts/${c.title || 'default'}`;

    this.isLoading.set(true);
    this.error.set(null);

    this.dashboardService.getChartData({ name: chartName }).subscribe({
      next: (data) => {
        this.chartData.set(data);
        this.isLoading.set(false);
      },
      error: (err) => {
        this.error.set(err?.message || 'Failed to load chart metrics');
        this.isLoading.set(false);
      },
    });
  }

  getPlotData(plotId: string): PlotData | undefined {
    const plotsData = this.chartData()?.plotsData;
    return plotsData?.find((p) => p.plotId === plotId);
  }

  onRemove(): void {
    const name = this.chart().name || `${this.chart().xid}`;
    this.remove.emit(name);
  }
}
