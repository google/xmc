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
import { ChangeDetectionStrategy, Component, input, output } from '@angular/core';

import { Chart } from 'xmanager_cloud/ui/app/service/dashboard_models';

import { ChartCardComponent, getChartKey } from '../chart_card/chart_card';

/**
 * Dashboard grid component rendering comparative experiment chart columns.
 */
@Component({
  selector: 'xmc-dashboard-grid',
  standalone: true,
  templateUrl: './dashboard_grid.ng.html',
  styleUrl: './dashboard_grid.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [CommonModule, ChartCardComponent],
})
export class DashboardGridComponent {
  readonly charts = input.required<Chart[]>();
  readonly removeChart = output<string>();

  protected readonly getChartKey = getChartKey;

  onRemoveChart(chartName: string): void {
    this.removeChart.emit(chartName);
  }
}
