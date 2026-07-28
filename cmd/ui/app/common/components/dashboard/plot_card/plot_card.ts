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
import { Component, computed, input } from '@angular/core';

import { PlotData, PlotSpec, PlotType } from 'xmanager_cloud/ui/app/service/dashboard_types';

import { XmcEchartsWrapperComponent } from '../echarts_wrapper/echarts_wrapper';
import { XmcSummaryBoxCardComponent } from '../summary_box_card/summary_box_card';
import { buildEChartsOptions } from './plot_chart_helpers';

/**
 * Renders an individual plot within a Chart card based on its Grammar of Graphics PlotType.
 */
@Component({
  selector: 'xmc-plot-card',
  standalone: true,
  imports: [XmcEchartsWrapperComponent, XmcSummaryBoxCardComponent],
  templateUrl: './plot_card.ng.html',
  styleUrl: './plot_card.scss',
})
export class XmcPlotCardComponent {
  readonly plotSpec = input.required<PlotSpec>();
  readonly plotData = input<PlotData>();
  readonly loading = input<boolean>(false);

  protected readonly PlotType = PlotType;

  protected readonly chartOptions = computed(() => {
    const data = this.plotData();
    const spec = this.plotSpec();
    return buildEChartsOptions(spec, data);
  });
}
