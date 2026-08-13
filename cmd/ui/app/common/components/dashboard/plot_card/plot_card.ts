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
  AfterViewInit,
  ChangeDetectionStrategy,
  Component,
  computed,
  ElementRef,
  input,
  OnDestroy,
  output,
  ViewChild,
} from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatTooltipModule } from '@angular/material/tooltip';
import * as echarts from 'echarts';

import {
  PlotData,
  PlotSpec,
  PlotType,
  SummaryBoxValue,
} from 'xmanager_cloud/ui/app/service/dashboard_models';

import { buildEChartsOption } from '../echarts_options_builder';
import { DEFAULT_XMC_THEME_CONFIG, XmcChartThemeConfig } from '../echarts_theme';
import { SummaryBoxComponent } from '../summary_box/summary_box';

/**
 * Plot card rendering either an ECharts canvas plot (Scatter/Bar) or a KPI Summary box.
 */
@Component({
  selector: 'xmc-plot-card',
  standalone: true,
  templateUrl: './plot_card.ng.html',
  styleUrl: './plot_card.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    MatCardModule,
    MatButtonModule,
    MatIconModule,
    MatMenuModule,
    MatTooltipModule,
    SummaryBoxComponent,
  ],
})
export class PlotCardComponent implements AfterViewInit, OnDestroy {
  readonly plotSpec = input.required<PlotSpec>();
  readonly plotData = input<PlotData>();
  readonly themeConfig = input<XmcChartThemeConfig>(DEFAULT_XMC_THEME_CONFIG);

  readonly exportImage = output<string>();

  @ViewChild('chartCanvas') chartCanvas?: ElementRef<HTMLDivElement>;

  private chartInstance?: echarts.ECharts;
  private resizeObserver?: ResizeObserver;

  protected readonly isSummaryBox = computed(() => {
    const type = this.plotSpec().type;
    return type === PlotType.SUMMARY_BOX || type === 1 || this.plotData()?.type === 'SUMMARY_BOX';
  });

  protected readonly summaryValue = computed<SummaryBoxValue | undefined>(() => {
    const pd = this.plotData();
    return pd?.summaryData || pd?.content?.summaryData;
  });

  protected readonly plotTitle = computed(() => {
    return this.plotSpec().title || this.plotSpec().plotId;
  });

  protected readonly infoTooltip = computed(() => {
    const spec = this.plotSpec();
    const metrics = spec.data?.metrics?.join(', ') || '';
    return metrics ? `Metrics: ${metrics}` : 'Metrics visualization';
  });

  ngAfterViewInit(): void {
    if (this.isSummaryBox()) return;
    this.initChart();
  }

  ngOnDestroy(): void {
    this.cleanupChart();
  }

  /**
   * Initializes the ECharts instance on the canvas container element.
   */
  private initChart(): void {
    if (!this.chartCanvas?.nativeElement) return;

    this.chartInstance = echarts.init(this.chartCanvas.nativeElement);
    this.renderChart();

    if (typeof ResizeObserver !== 'undefined') {
      this.resizeObserver = new ResizeObserver(() => {
        this.chartInstance?.resize();
      });
      this.resizeObserver.observe(this.chartCanvas.nativeElement);
    }
  }

  /**
   * Updates chart options using the options builder.
   */
  protected renderChart(): void {
    if (!this.chartInstance) return;
    const option = buildEChartsOption(this.plotSpec(), this.plotData(), this.themeConfig());
    this.chartInstance.setOption(option, true);
  }

  protected onExportPng(): void {
    if (!this.chartInstance) return;
    const dataUrl = this.chartInstance.getDataURL({ type: 'png', pixelRatio: 2 });
    this.exportImage.emit(dataUrl);
  }

  private cleanupChart(): void {
    this.resizeObserver?.disconnect();
    this.resizeObserver = undefined;
    this.chartInstance?.dispose();
    this.chartInstance = undefined;
  }
}
