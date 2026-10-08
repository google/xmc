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
  GeomSpec,
  GeomType,
  PlotData,
  PlotSpec,
  SeriesData,
  SeriesPoint,
} from 'app/service/dashboard_models';

import {
  DEFAULT_XMC_THEME_CONFIG,
  XMC_BASELINE_COLOR,
  XMC_SERIES_PALETTE,
  XMC_SERIES_SYMBOLS,
  XmcChartThemeConfig,
} from './echarts_theme';

/**
 * Builds ECharts options for Scatter / Time-Series metrics plots.
 */
export function buildScatterOption(
  spec: PlotSpec,
  data?: PlotData,
  theme: XmcChartThemeConfig = DEFAULT_XMC_THEME_CONFIG,
  baseLayout: Partial<EChartsOption> = {},
): EChartsOption {
  const seriesList = extractSeriesList(data);
  if (!seriesList.length) {
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

  const seriesOptions: SeriesOption[] = seriesList.map((s, idx) =>
    buildSingleScatterSeries(s, idx, s.isBaseline || s.seriesLabel?.toLowerCase() === 'baseline'),
  );
  seriesOptions.push(...extractHlineSeries(spec.geoms));

  return {
    ...baseLayout,
    ...getScatterAxes(spec, theme),
    series: seriesOptions,
  };
}

export function extractSeriesList(data?: PlotData): SeriesData[] {
  if (!data) return [];
  const rawSeries = data.scatterSeries || data.content?.scatterSeries;
  return data.seriesList ?? (rawSeries ? [rawSeries] : []);
}

export function parseNumericStep(val?: number | string | null): number | undefined {
  if (val === undefined || val === null) return undefined;
  const num = typeof val === 'number' ? val : parseFloat(val);
  return isNaN(num) ? 0 : num;
}

export function getPointStep(point: SeriesPoint): number {
  const step = parseNumericStep(point.step);
  if (step !== undefined) return step;

  const iteration = parseNumericStep(point.iteration);
  if (iteration !== undefined) return iteration;

  if (point.timestamp) {
    const parsedTime = Date.parse(point.timestamp);
    if (!isNaN(parsedTime)) return parsedTime;
  }
  return 0;
}

export function extractHlineSeries(geoms?: GeomSpec[]): SeriesOption[] {
  if (!geoms) return [];
  return geoms
    .filter((g) => g.type === GeomType.HLINE && g.yintercept != null)
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

export function getScatterAxes(spec: PlotSpec, theme: XmcChartThemeConfig) {
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
