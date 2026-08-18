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

import {
  BarData,
  Chart,
  ChartData,
  Dashboard,
  DataSpec,
  PlotData,
  PlotSpec,
  PlotType,
  RawBarData,
  RawChart,
  RawChartData,
  RawDashboard,
  RawPlotData,
  RawPlotSpec,
  RawSeriesData,
  RawSeriesPoint,
  RawSummaryBoxValue,
  SeriesData,
  SeriesPoint,
  SummaryBoxValue,
} from './dashboard_models';

function assignDefined<T extends object>(target: T, source: Partial<T>): void {
  for (const [key, value] of Object.entries(source)) {
    if (value !== undefined && value !== null) {
      (target as Record<string, unknown>)[key] = value;
    }
  }
}

function extractNumericSummaryValues(raw: RawSummaryBoxValue): Partial<SummaryBoxValue> {
  return {
    latestValue: raw.latestValue ?? raw.latest_value,
    minValue: raw.minValue ?? raw.min_value,
    maxValue: raw.maxValue ?? raw.max_value,
    meanValue: raw.meanValue ?? raw.mean_value,
    baselineValue: raw.baselineValue ?? raw.baseline_value,
    polarity: raw.polarity,
  };
}

function extractMetaSummaryValues(raw: RawSummaryBoxValue): Partial<SummaryBoxValue> {
  return {
    metricName: raw.metricName ?? raw.metric_name,
    latestStep: raw.latestStep ?? raw.latest_step,
    latestIteration: raw.latestIteration ?? raw.latest_iteration,
    latestTimestamp: raw.latestTimestamp ?? raw.latest_timestamp,
    deltaVsBaseline: raw.deltaVsBaseline ?? raw.delta_vs_baseline,
    deltaPercentVsBaseline: raw.deltaPercentVsBaseline ?? raw.delta_percent_vs_baseline,
  };
}

export function normalizeSummaryBoxValue(raw?: RawSummaryBoxValue): SummaryBoxValue | undefined {
  if (!raw) return undefined;
  const result: SummaryBoxValue = {};
  assignDefined(result, extractNumericSummaryValues(raw));
  assignDefined(result, extractMetaSummaryValues(raw));
  return result;
}

export function normalizeSeriesPoint(p: RawSeriesPoint): SeriesPoint {
  const pt: SeriesPoint = {};
  assignDefined(pt, {
    step: p.step,
    iteration: p.iteration,
    timestamp: p.timestamp,
    avgValue: p.avgValue ?? p.avg_value,
    minValue: p.minValue ?? p.min_value,
    maxValue: p.maxValue ?? p.max_value,
  });
  return pt;
}

export function normalizeSeriesData(s?: RawSeriesData): SeriesData | undefined {
  if (!s) return undefined;
  const res: SeriesData = {
    points: (s.points ?? []).map(normalizeSeriesPoint),
  };
  assignDefined(res, {
    metricName: s.metricName ?? s.metric_name,
    workUnitId: s.workUnitId ?? s.work_unit_id,
    xid: parseNumericId(s.xid),
    seriesLabel: s.seriesLabel ?? s.series_label,
    isBaseline: s.isBaseline ?? s.is_baseline,
  });
  return res;
}

function extractRawSummary(pd: RawPlotData): RawSummaryBoxValue | undefined {
  return pd.summaryData ?? pd.summary_data ?? pd.content?.summaryData ?? pd.content?.summary_data;
}

function extractRawScatter(pd: RawPlotData): RawSeriesData | undefined {
  return (
    pd.scatterSeries ?? pd.scatter_series ?? pd.content?.scatterSeries ?? pd.content?.scatter_series
  );
}

function extractRawBar(pd: RawPlotData): RawBarData | undefined {
  return pd.barData ?? pd.bar_data ?? pd.content?.barData ?? pd.content?.bar_data;
}

function normalizeRawBarData(rawBar?: RawBarData): BarData | undefined {
  if (!rawBar) return undefined;
  return {
    metricName: rawBar.metricName ?? rawBar.metric_name,
    buckets: (rawBar.buckets ?? []).map((b) => ({
      categoryOrBin: b.categoryOrBin ?? b.category_or_bin,
      valueOrCount: b.valueOrCount ?? b.value_or_count,
    })),
  };
}

export function normalizePlotData(pd?: RawPlotData): PlotData | undefined {
  if (!pd) return undefined;
  const rawSeriesList = pd.seriesList ?? pd.series_list;
  const seriesList = rawSeriesList?.map(normalizeSeriesData).filter((s): s is SeriesData => !!s);

  const result: PlotData = {
    plotId: pd.plotId ?? pd.plot_id ?? '',
    type: pd.type,
  };
  assignDefined(result, {
    summaryData: normalizeSummaryBoxValue(extractRawSummary(pd)),
    scatterSeries: normalizeSeriesData(extractRawScatter(pd)),
    barData: normalizeRawBarData(extractRawBar(pd)),
    seriesList,
  });
  return result;
}

export function normalizeChartData(cd?: RawChartData): ChartData | undefined {
  if (!cd) return undefined;
  const rawPlots = cd.plotsData ?? cd.plots_data ?? [];
  return {
    name: cd.name,
    xid: parseNumericId(cd.xid),
    wid: parseNumericId(cd.wid),
    plotsData: rawPlots.map((p) => normalizePlotData(p)).filter(Boolean) as PlotData[],
  };
}

export function normalizePlotSpec(p?: RawPlotSpec): PlotSpec {
  if (!p) return { plotId: '', type: PlotType.SCATTER, data: { metrics: [] } };
  const data: DataSpec = { metrics: p.data?.metrics ?? [] };
  if (p.data?.ranges !== undefined) {
    data.ranges = p.data.ranges;
  }
  const result: PlotSpec = {
    plotId: p.plotId ?? p.plot_id ?? '',
    type: p.type ?? PlotType.SCATTER,
    data,
  };
  assignDefined(result, {
    title: p.title,
    mapping: p.mapping,
    geoms: p.geoms,
    stats: p.stats,
    coords: p.coords,
    facets: p.facets,
  });
  return result;
}

export function parseNumericId(id?: number | string | null): number | undefined {
  if (id === undefined || id === null || id === '') return undefined;
  if (typeof id === 'number') {
    return isNaN(id) ? undefined : id;
  }
  if (typeof id === 'string') {
    const parsed = parseInt(id, 10);
    return isNaN(parsed) ? undefined : parsed;
  }
  return undefined;
}

export function normalizeChart(c?: RawChart): Chart {
  if (!c) return { title: 'Experiment 0', xid: 0, plots: [] };
  const xid = parseNumericId(c.xid) ?? 0;
  const wid = parseNumericId(c.wid);
  const result: Chart = {
    title: c.title ?? `Experiment ${xid}`,
    xid,
    plots: (c.plots ?? []).map(normalizePlotSpec),
  };
  assignDefined(result, {
    name: c.name,
    description: c.description,
    wid,
    plotLayout: c.plotLayout ?? c.plot_layout,
    createTime: c.createTime ?? c.create_time,
    updateTime: c.updateTime ?? c.update_time,
  });
  return result;
}

export function normalizeDashboard(d?: RawDashboard): Dashboard {
  if (!d) return { title: 'Dashboard' };
  const result: Dashboard = {
    title: d.title ?? d.name ?? 'Dashboard',
  };
  assignDefined(result, {
    name: d.name,
    description: d.description,
    owner: d.owner,
    charts: d.charts?.map(normalizeChart),
    chartLayout: d.chartLayout ?? d.chart_layout,
    createTime: d.createTime ?? d.create_time,
    updateTime: d.updateTime ?? d.update_time,
  });
  return result;
}
