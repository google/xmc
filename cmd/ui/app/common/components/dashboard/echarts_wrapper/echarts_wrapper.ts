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
import { Component, effect, ElementRef, input, OnDestroy, OnInit, viewChild } from '@angular/core';
import { BarChart, LineChart, ScatterChart } from 'echarts/charts';
import {
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from 'echarts/components';
import * as echarts from 'echarts/core';
import { CanvasRenderer } from 'echarts/renderers';

echarts.use([
  CanvasRenderer,
  LineChart,
  BarChart,
  ScatterChart,
  GridComponent,
  TooltipComponent,
  DataZoomComponent,
  LegendComponent,
]);

/**
 * Reusable wrapper around Apache ECharts for rendering scientific time series and bar plots.
 */
@Component({
  selector: 'xmc-echarts-wrapper',
  standalone: true,
  templateUrl: './echarts_wrapper.ng.html',
  styleUrl: './echarts_wrapper.scss',
})
export class XmcEchartsWrapperComponent implements OnInit, OnDestroy {
  readonly options = input.required<echarts.EChartsCoreOption>();
  readonly loading = input<boolean>(false);

  readonly containerRef = viewChild.required<ElementRef<HTMLDivElement>>('canvasContainer');

  private chartInstance?: echarts.ECharts;
  private resizeObserver?: ResizeObserver;

  constructor() {
    effect(() => {
      const opts = this.options();
      if (this.chartInstance && opts) {
        this.chartInstance.setOption(opts, { notMerge: false });
      }
    });

    effect(() => {
      const isLoading = this.loading();
      if (this.chartInstance) {
        if (isLoading) {
          this.chartInstance.showLoading();
        } else {
          this.chartInstance.hideLoading();
        }
      }
    });
  }

  ngOnInit(): void {
    const el = this.containerRef().nativeElement;
    this.chartInstance = echarts.init(el);
    if (this.options()) {
      this.chartInstance.setOption(this.options());
    }

    this.resizeObserver = new ResizeObserver(() => {
      this.chartInstance?.resize();
    });
    this.resizeObserver.observe(el);
  }

  ngOnDestroy(): void {
    this.resizeObserver?.disconnect();
    this.chartInstance?.dispose();
  }

  /** Returns internal ECharts instance for testing. */
  getChartInstance(): echarts.ECharts | undefined {
    return this.chartInstance;
  }
}
