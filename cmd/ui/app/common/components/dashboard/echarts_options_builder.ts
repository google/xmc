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

import type { EChartsOption, SeriesOption } from 'echarts';

import {
  BarData,
  GeomSpec,
  GeomType,
  PlotData,
  PlotSpec,
  PlotType,
  SeriesData,
  SeriesPoint,
} from 'xmanager_cloud/ui/app/service/dashboard_models';

import {
  DEFAULT_XMC_THEME_CONFIG,
  XMC_BASELINE_COLOR,
  XMC_SERIES_PALETTE,
  XMC_SERIES_SYMBOLS,
  XmcChartThemeConfig,
} from './echarts_theme';

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

function getPointStep(point: SeriesPoint): number {
  if (point.step !== undefined && point.step !== null) {
    return typeof point.step === 'string' ? parseFloat(point.step) : point.step;
  }
  if (point.iteration !== undefined && point.iteration !== null) {
    return typeof point.iteration === 'string' ? parseFloat(point.iteration) : point.iteration;
  }
  return 0;
}

function buildBaselineScatterSeries(name: string, coordinates: number[][]): SeriesOption {
  return {
    name,
    type: 'line',
    data: coordinates,
    smooth: false,
    showSymbol: false,
    lineStyle: { color: XMC_BASELINE_COLOR, width: 2, type: 'dotted' },
    itemStyle: { color: XMC_BASELINE_COLOR },
  };
}

function buildNormalScatterSeries(
  name: string,
  coordinates: number[][],
  index: number,
): SeriesOption {
  const color = XMC_SERIES_PALETTE[index % XMC_SERIES_PALETTE.length];
  const symbol = XMC_SERIES_SYMBOLS[index % XMC_SERIES_SYMBOLS.length];

  return {
    name,
    type: 'line',
    data: coordinates,
    smooth: false,
    showSymbol: coordinates.length <= 50,
    symbol,
    symbolSize: 6,
    lineStyle: { color, width: 2 },
    itemStyle: { color },
  };
}

function buildSingleScatterSeries(
  series: SeriesData,
  index: number,
  isBaseline = false,
): SeriesOption {
  const points = series.points ?? [];
  const coordinates = points.map((p) => [getPointStep(p), p.avgValue ?? 0]);
  const seriesName = series.seriesLabel || series.metricName || `Run ${index + 1}`;

  if (isBaseline || series.isBaseline) {
    return buildBaselineScatterSeries(seriesName, coordinates);
  }
  return buildNormalScatterSeries(seriesName, coordinates, index);
}

function extractHlineSeries(geoms?: GeomSpec[]): SeriesOption[] {
  if (!geoms) return [];
  return geoms
    .filter((g) => g.type === GeomType.HLINE && g.yintercept !== undefined)
    .map((g) => ({
      name: 'Baseline',
      type: 'line',
      markLine: {
        symbol: 'none',
        lineStyle: {
          color: g.style?.color || XMC_BASELINE_COLOR,
          type: (g.style?.lineStyle as 'solid' | 'dashed' | 'dotted') || 'dotted',
          width: g.style?.size || 2,
        },
        data: [{ yAxis: g.yintercept! }],
      },
    }));
}

function getBaseChartLayout(theme: XmcChartThemeConfig): Partial<EChartsOption> {
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

function getScatterAxes(spec: PlotSpec, theme: XmcChartThemeConfig) {
  return {
    xAxis: {
      type: 'value' as const,
      axisLine: { lineStyle: { color: '#dadce0' } },
      axisLabel: { color: theme.axisTextColor, fontSize: 11 },
      splitLine: { show: false },
    },
    yAxis: {
      type: 'value' as const,
      min: spec.coords?.yMin,
      max: spec.coords?.yMax,
      axisLine: { show: false },
      axisTick: { show: false },
      axisLabel: { color: theme.axisTextColor, fontSize: 11 },
      splitLine: { lineStyle: { color: theme.gridLineColor } },
    },
  };
}

/**
 * Builds ECharts options for Scatter / Time-Series metrics plots.
 */
export function buildScatterOption(
  spec: PlotSpec,
  data?: PlotData,
  theme: XmcChartThemeConfig = DEFAULT_XMC_THEME_CONFIG,
): EChartsOption {
  const rawSeries = data?.scatterSeries || data?.content?.scatterSeries;
  const seriesList = data?.seriesList ?? (rawSeries ? [rawSeries] : []);
  if (!seriesList.length) return getEmptyChartOption();

  const seriesOptions: SeriesOption[] = seriesList.map((s, idx) =>
    buildSingleScatterSeries(s, idx, s.isBaseline || s.seriesLabel?.toLowerCase() === 'baseline'),
  );
  seriesOptions.push(...extractHlineSeries(spec.geoms));

  return {
    ...getBaseChartLayout(theme),
    ...getScatterAxes(spec, theme),
    series: seriesOptions,
  };
}

/**
 * Builds ECharts options for Bar chart plots.
 */
export function buildBarOption(
  spec: PlotSpec,
  data?: PlotData,
  theme: XmcChartThemeConfig = DEFAULT_XMC_THEME_CONFIG,
): EChartsOption {
  const barData: BarData | undefined = data?.barData || data?.content?.barData;
  const buckets = barData?.buckets ?? [];
  if (!buckets.length) return getEmptyChartOption();

  return {
    ...getBaseChartLayout(theme),
    xAxis: {
      type: 'category',
      data: buckets.map((b) => b.categoryOrBin ?? ''),
      axisLabel: { color: theme.axisTextColor, fontSize: 11 },
      axisLine: { lineStyle: { color: '#dadce0' } },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: theme.axisTextColor, fontSize: 11 },
      splitLine: { lineStyle: { color: theme.gridLineColor } },
    },
    series: [
      {
        type: 'bar',
        data: buckets.map((b) => b.valueOrCount ?? 0),
        itemStyle: { color: XMC_SERIES_PALETTE[0], borderRadius: [4, 4, 0, 0] },
      },
    ],
  };
}

function isScatterType(spec: PlotSpec, data: PlotData): boolean {
  return spec.type === PlotType.SCATTER || spec.type === 2 || data.type === 'SCATTER';
}

function isBarType(spec: PlotSpec, data: PlotData): boolean {
  return (
    spec.type === PlotType.BAR ||
    spec.type === 3 ||
    spec.type === PlotType.HISTOGRAM ||
    spec.type === 4 ||
    data.type === 'BAR'
  );
}

/**
 * Universal option builder converting PlotSpec + PlotData into EChartsOption.
 */
export function buildEChartsOption(
  spec: PlotSpec,
  data?: PlotData,
  themeConfig: XmcChartThemeConfig = DEFAULT_XMC_THEME_CONFIG,
): EChartsOption {
  if (!data) return getEmptyChartOption();
  if (isScatterType(spec, data)) return buildScatterOption(spec, data, themeConfig);
  if (isBarType(spec, data)) return buildBarOption(spec, data, themeConfig);
  return getEmptyChartOption();
}
