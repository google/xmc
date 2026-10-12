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

import { PlotData, PlotSpec, PlotType } from 'app/service/dashboard_models';

import { buildBarOption } from './bar_options_builder';
import { DEFAULT_XMC_THEME_CONFIG, XmcChartThemeConfig } from './echarts_theme';
import { buildScatterOption } from './scatter_options_builder';

// Re-export dedicated builders for clean API access
export {
  buildBarOption,
  extractBarData,
  getBarAxes,
  getBarSeriesName,
} from './bar_options_builder';
export {
  buildScatterOption,
  extractHlineSeries,
  extractSeriesList,
  getPointStep,
  getScatterAxes,
  parseNumericStep,
} from './scatter_options_builder';

/**
 * Universal option builder converting PlotSpec + PlotData into EChartsOption.
 */
export function buildEChartsOption(
  spec: PlotSpec,
  data?: PlotData,
  themeConfig: XmcChartThemeConfig = DEFAULT_XMC_THEME_CONFIG,
): EChartsOption {
  if (!data) return getEmptyChartOption();
  const baseLayout = getBaseChartLayout(themeConfig);
  if (isScatterType(spec, data)) {
    return buildScatterOption(spec, data, themeConfig, baseLayout);
  }
  if (isBarType(spec, data)) {
    return buildBarOption(spec, data, themeConfig, baseLayout);
  }
  return getEmptyChartOption();
}

/**
 * Returns a fallback empty EChartsOption configuration.
 */
export function getEmptyChartOption(): EChartsOption {
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

/**
 * Generates shared base layout options (grid, tooltip, legend, fonts).
 */
export function getBaseChartLayout(theme: XmcChartThemeConfig): Partial<EChartsOption> {
  return {
    backgroundColor: theme.backgroundColor,
    textStyle: { fontFamily: theme.fontFamily },
    grid: { left: 48, right: 20, top: 24, bottom: 40, containLabel: true },
    tooltip: {
      trigger: 'axis',
      backgroundColor: theme.tooltipBgColor,
      borderColor: theme.tooltipBgColor,
      textStyle: { color: theme.tooltipTextColor, fontFamily: theme.fontFamily, fontSize: 12 },
      axisPointer: { type: 'line', lineStyle: { color: '#1a73e8', width: 1.5, type: 'dashed' } },
    },
    legend: {
      bottom: 0,
      icon: 'circle',
      textStyle: { color: theme.axisTextColor, fontSize: 12 },
      itemWidth: 10,
      itemHeight: 10,
    },
  };
}

export function isScatterType(spec: PlotSpec, data: PlotData): boolean {
  return (
    spec.type === PlotType.SCATTER ||
    spec.type === 2 ||
    data.type === PlotType.SCATTER ||
    data.type === 2 ||
    data.type === 'SCATTER'
  );
}

export function isBarType(spec: PlotSpec, data: PlotData): boolean {
  return (
    spec.type === PlotType.BAR ||
    spec.type === 3 ||
    spec.type === PlotType.HISTOGRAM ||
    spec.type === 4 ||
    data.type === PlotType.BAR ||
    data.type === 3 ||
    data.type === PlotType.HISTOGRAM ||
    data.type === 4 ||
    data.type === 'BAR' ||
    data.type === 'HISTOGRAM'
  );
}
