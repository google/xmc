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

import type { EChartsOption } from 'echarts';

import { BarData, PlotData, PlotSpec } from 'app/service/dashboard_models';

import { DEFAULT_XMC_THEME_CONFIG, XMC_SERIES_PALETTE, XmcChartThemeConfig } from './echarts_theme';

/**
 * Builds ECharts options for Bar chart plots.
 */
export function buildBarOption(
  spec: PlotSpec,
  data?: PlotData,
  theme: XmcChartThemeConfig = DEFAULT_XMC_THEME_CONFIG,
  baseLayout: Partial<EChartsOption> = {},
): EChartsOption {
  const barData = extractBarData(data);
  const buckets = barData?.buckets ?? [];
  if (!buckets.length) return getEmptyBarOption();

  const categories = buckets.map((b) => b.categoryOrBin ?? '');
  const values = buckets.map((b) => b.valueOrCount ?? 0);
  const seriesName = getBarSeriesName(spec, barData);

  return {
    ...baseLayout,
    ...getBarAxes(spec, theme, categories),
    series: [
      {
        name: seriesName,
        type: 'bar',
        data: values,
        itemStyle: { color: XMC_SERIES_PALETTE[0], borderRadius: [4, 4, 0, 0] },
      },
    ],
  };
}

export function extractBarData(data?: PlotData): BarData | undefined {
  return data?.barData || data?.content?.barData;
}

export function getBarSeriesName(spec: PlotSpec, barData?: BarData): string {
  return spec.title || barData?.metricName || 'Value';
}

export function getBarAxes(spec: PlotSpec, theme: XmcChartThemeConfig, categories: string[]) {
  return {
    xAxis: {
      type: 'category' as const,
      data: categories,
      axisLabel: { color: theme.axisTextColor, fontSize: 11 },
      axisLine: { lineStyle: { color: '#dadce0' } },
    },
    yAxis: {
      type: 'value' as const,
      min: spec.coords?.yMin,
      max: spec.coords?.yMax,
      axisLabel: { color: theme.axisTextColor, fontSize: 11 },
      splitLine: { lineStyle: { color: theme.gridLineColor } },
    },
  };
}

function getEmptyBarOption(): EChartsOption {
  return {
    title: {
      text: 'No metric data available',
      left: 'center',
      top: 'middle',
      textStyle: { color: '#9aa0a6', fontSize: 13, fontWeight: 'normal' },
    },
    xAxis: { show: false },
    yAxis: { show: false },
    series: [],
  };
}
