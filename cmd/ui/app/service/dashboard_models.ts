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

/**
 * Enums representing Plot Types in XMC.
 */
export enum PlotType {
  PLOT_TYPE_UNSPECIFIED = 0,
  SUMMARY_BOX = 1,
  SCATTER = 2,
  BAR = 3,
  HISTOGRAM = 4,
}

/**
 * Enums representing Layout Types for Dashboards and Charts.
 */
export enum LayoutType {
  LAYOUT_TYPE_UNSPECIFIED = 0,
  GRID = 1,
  WRAP = 2,
  SIDE_BY_SIDE = 3,
  STACKED = 4,
}

/**
 * Enums representing Geometries.
 */
export enum GeomType {
  GEOM_TYPE_UNSPECIFIED = 0,
  LINE = 1,
  POINT = 2,
  BAR = 3,
  AREA = 4,
  RIBBON = 5,
  HLINE = 6,
  VLINE = 7,
}

/**
 * Metric polarity indicating whether lower or higher values represent improvement.
 */
export enum MetricPolarity {
  LOWER_IS_BETTER = 'LOWER_IS_BETTER',
  HIGHER_IS_BETTER = 'HIGHER_IS_BETTER',
  NEUTRAL = 'NEUTRAL',
}

export interface GridPosition {
  itemId?: string;
  x?: number;
  y?: number;
  width?: number;
  height?: number;
}

export interface LayoutSpec {
  type: LayoutType | number;
  columns?: number;
  positions?: GridPosition[];
}

export interface DataRange {
  startStep?: number | string;
  startIteration?: number | string;
  startTime?: string;
  endStep?: number | string;
  endIteration?: number | string;
  endTime?: string;
}

export interface DataSpec {
  metrics: string[];
  ranges?: DataRange[];
}

export interface MappingSpec {
  x?: string;
  y?: string;
  color?: string;
  group?: string;
  size?: string;
}

export interface StyleSpec {
  color?: string;
  opacity?: number;
  size?: number;
  lineStyle?: string;
}

export interface GeomSpec {
  type: GeomType | number;
  yintercept?: number;
  xintercept?: number;
  style?: StyleSpec;
}

export interface SmoothConfig {
  algorithm: number;
  alpha?: number;
  windowSize?: number;
}

export interface ForecastConfig {
  alpha?: number;
  beta?: number;
  stepsAhead: number;
}

export interface StatSpec {
  type: number;
  aggregation?: number;
  smooth?: SmoothConfig;
  forecast?: ForecastConfig;
}

export interface CoordSpec {
  xScale?: number;
  yScale?: number;
  yMin?: number;
  yMax?: number;
}

export interface FacetSpec {
  type: number;
  facetBy: string[];
  ncol?: number;
  nrow?: number;
}

export interface PlotSpec {
  plotId: string;
  title?: string;
  type: PlotType | number;
  data: DataSpec;
  mapping?: MappingSpec;
  geoms?: GeomSpec[];
  stats?: StatSpec;
  coords?: CoordSpec;
  facets?: FacetSpec;
}

export interface Chart {
  name?: string;
  title: string;
  description?: string;
  xid: number;
  wid?: number;
  plots: PlotSpec[];
  plotLayout?: LayoutSpec;
  createTime?: string;
  updateTime?: string;
}

export interface Dashboard {
  name?: string;
  title: string;
  description?: string;
  owner?: string;
  charts?: Chart[];
  chartLayout?: LayoutSpec;
  createTime?: string;
  updateTime?: string;
}

export interface SummaryBoxValue {
  metricName?: string;
  latestValue?: number;
  minValue?: number;
  maxValue?: number;
  meanValue?: number;
  latestStep?: number | string;
  latestIteration?: number | string;
  latestTimestamp?: string;
  deltaVsBaseline?: number;
  deltaPercentVsBaseline?: number;
  baselineValue?: number;
  polarity?: MetricPolarity;
}

export interface SeriesPoint {
  step?: number | string;
  iteration?: number | string;
  timestamp?: string;
  avgValue?: number;
  minValue?: number;
  maxValue?: number;
}

export interface SeriesData {
  metricName?: string;
  workUnitId?: string;
  xid?: number | string;
  points?: SeriesPoint[];
  seriesLabel?: string;
  isBaseline?: boolean;
}

export interface BarBucket {
  categoryOrBin?: string;
  valueOrCount?: number;
}

export interface BarData {
  metricName?: string;
  buckets?: BarBucket[];
}

export interface PlotData {
  plotId?: string;
  type?: PlotType | string | number;
  summaryData?: SummaryBoxValue;
  scatterSeries?: SeriesData;
  barData?: BarData;
  seriesList?: SeriesData[];
  content?: {
    summaryData?: SummaryBoxValue;
    scatterSeries?: SeriesData;
    barData?: BarData;
  };
}

export interface ChartData {
  name?: string;
  xid?: number | string;
  wid?: number | string;
  plotsData?: PlotData[];
}

export interface CreateDashboardRequest {
  dashboard: Dashboard;
  dashboardId?: string;
}

export interface UpdateDashboardRequest {
  dashboard: Dashboard;
  updateMask?: string;
}

export interface ListDashboardsRequest {
  pageSize?: number;
  pageToken?: string;
  filter?: string;
}

export interface ListDashboardsResponse {
  dashboards?: Dashboard[];
  nextPageToken?: string;
}

export interface CreateChartRequest {
  parent: string;
  chart: Chart;
  chartId?: string;
}

export interface GetChartDataRequest {
  name: string;
  parentExperiment?: string;
  parentWorkunit?: string;
  chartWidthPixels?: number;
}

export interface RawSummaryBoxValue {
  metricName?: string;
  metric_name?: string;
  latestValue?: number;
  latest_value?: number;
  minValue?: number;
  min_value?: number;
  maxValue?: number;
  max_value?: number;
  meanValue?: number;
  mean_value?: number;
  latestStep?: number | string;
  latest_step?: number | string;
  latestIteration?: number | string;
  latest_iteration?: number | string;
  latestTimestamp?: string;
  latest_timestamp?: string;
  deltaVsBaseline?: number;
  delta_vs_baseline?: number;
  deltaPercentVsBaseline?: number;
  delta_percent_vs_baseline?: number;
  baselineValue?: number;
  baseline_value?: number;
  polarity?: MetricPolarity;
}

export interface RawSeriesPoint {
  step?: number | string;
  iteration?: number | string;
  timestamp?: string;
  avgValue?: number;
  avg_value?: number;
  minValue?: number;
  min_value?: number;
  maxValue?: number;
  max_value?: number;
}

export interface RawSeriesData {
  metricName?: string;
  metric_name?: string;
  workUnitId?: string;
  work_unit_id?: string;
  xid?: number | string;
  points?: RawSeriesPoint[];
  seriesLabel?: string;
  series_label?: string;
  isBaseline?: boolean;
  is_baseline?: boolean;
}

export interface RawBarBucket {
  categoryOrBin?: string;
  category_or_bin?: string;
  valueOrCount?: number;
  value_or_count?: number;
}

export interface RawBarData {
  metricName?: string;
  metric_name?: string;
  buckets?: RawBarBucket[];
}

export interface RawPlotData {
  plotId?: string;
  plot_id?: string;
  type?: PlotType | string | number;
  summaryData?: RawSummaryBoxValue;
  summary_data?: RawSummaryBoxValue;
  scatterSeries?: RawSeriesData;
  scatter_series?: RawSeriesData;
  barData?: RawBarData;
  bar_data?: RawBarData;
  seriesList?: RawSeriesData[];
  series_list?: RawSeriesData[];
  content?: {
    summaryData?: RawSummaryBoxValue;
    summary_data?: RawSummaryBoxValue;
    scatterSeries?: RawSeriesData;
    scatter_series?: RawSeriesData;
    barData?: RawBarData;
    bar_data?: RawBarData;
  };
}

export interface RawChartData {
  name?: string;
  xid?: number | string;
  wid?: number | string;
  plotsData?: RawPlotData[];
  plots_data?: RawPlotData[];
}

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

export interface RawPlotSpec {
  plotId?: string;
  plot_id?: string;
  title?: string;
  type?: PlotType | number;
  data?: DataSpec;
  mapping?: MappingSpec;
  geoms?: GeomSpec[];
  stats?: StatSpec;
  coords?: CoordSpec;
  facets?: FacetSpec;
}

export interface RawChart {
  name?: string;
  title?: string;
  description?: string;
  xid?: number | string;
  wid?: number | string;
  plots?: RawPlotSpec[];
  plotLayout?: LayoutSpec;
  plot_layout?: LayoutSpec;
  createTime?: string;
  create_time?: string;
  updateTime?: string;
  update_time?: string;
}

export interface RawDashboard {
  name?: string;
  title?: string;
  description?: string;
  owner?: string;
  charts?: RawChart[];
  chartLayout?: LayoutSpec;
  chart_layout?: LayoutSpec;
  createTime?: string;
  create_time?: string;
  updateTime?: string;
  update_time?: string;
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
