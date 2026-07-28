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
import * as echarts from 'echarts/core';

import {
  BarData,
  PlotData,
  PlotSpec,
  PlotType,
  SeriesData,
} from 'xmanager_cloud/ui/app/service/dashboard_types';

/**
 * Builds ECharts configuration option for SCATTER / line plots.
 */
function buildScatterOption(series?: SeriesData): echarts.EChartsCoreOption {
  const points = series?.points ?? [];
  const lineData = points.map((p) => [p.iteration, p.avgValue]);
  return {
    tooltip: { trigger: 'axis' },
    grid: { left: 48, right: 24, top: 24, bottom: 48 },
    xAxis: { type: 'value', name: 'Step' },
    yAxis: { type: 'value', name: series?.metricName ?? 'Value' },
    dataZoom: [
      { type: 'inside', start: 0, end: 100 },
      { type: 'slider', start: 0, end: 100 },
    ],
    series: [
      {
        name: series?.metricName ?? 'Metric',
        type: 'line',
        showSymbol: points.length < 50,
        smooth: true,
        data: lineData,
      },
    ],
  };
}

/**
 * Builds ECharts configuration option for categorical BAR plots.
 */
function buildBarOption(barData?: BarData): echarts.EChartsCoreOption {
  const buckets = barData?.buckets ?? [];
  return {
    tooltip: { trigger: 'axis' },
    grid: { left: 48, right: 24, top: 24, bottom: 48 },
    xAxis: {
      type: 'category',
      data: buckets.map((b) => b.categoryOrBin),
    },
    yAxis: { type: 'value' },
    series: [
      {
        name: barData?.metricName ?? 'Count',
        type: 'bar',
        data: buckets.map((b) => b.valueOrCount),
      },
    ],
  };
}

/**
 * Translates XMC PlotSpec and PlotData into an Apache ECharts configuration option.
 */
export function buildEChartsOptions(spec: PlotSpec, data?: PlotData): echarts.EChartsCoreOption {
  if (spec.type === PlotType.BAR) {
    return buildBarOption(data?.barData);
  }
  return buildScatterOption(data?.scatterSeries);
}
