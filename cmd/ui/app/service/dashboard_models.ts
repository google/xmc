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

export function normalizeSummaryBoxValue(raw?: RawSummaryBoxValue): SummaryBoxValue | undefined {
  if (!raw) return undefined;
  const result: SummaryBoxValue = {};

  const metric = raw.metricName ?? raw.metric_name;
  if (metric !== undefined) result.metricName = metric;

  const latest = raw.latestValue ?? raw.latest_value;
  if (latest !== undefined) result.latestValue = latest;

  const min = raw.minValue ?? raw.min_value;
  if (min !== undefined) result.minValue = min;

  const max = raw.maxValue ?? raw.max_value;
  if (max !== undefined) result.maxValue = max;

  const mean = raw.meanValue ?? raw.mean_value;
  if (mean !== undefined) result.meanValue = mean;

  const step = raw.latestStep ?? raw.latest_step;
  if (step !== undefined) result.latestStep = step;

  const iter = raw.latestIteration ?? raw.latest_iteration;
  if (iter !== undefined) result.latestIteration = iter;

  const ts = raw.latestTimestamp ?? raw.latest_timestamp;
  if (ts !== undefined) result.latestTimestamp = ts;

  const delta = raw.deltaVsBaseline ?? raw.delta_vs_baseline;
  if (delta !== undefined) result.deltaVsBaseline = delta;

  const deltaPct = raw.deltaPercentVsBaseline ?? raw.delta_percent_vs_baseline;
  if (deltaPct !== undefined) result.deltaPercentVsBaseline = deltaPct;

  const baseline = raw.baselineValue ?? raw.baseline_value;
  if (baseline !== undefined) result.baselineValue = baseline;

  if (raw.polarity !== undefined) result.polarity = raw.polarity;

  return result;
}

export function normalizeSeriesPoint(p: RawSeriesPoint): SeriesPoint {
  const pt: SeriesPoint = {};
  if (p.step !== undefined) pt.step = p.step;
  if (p.iteration !== undefined) pt.iteration = p.iteration;
  if (p.timestamp !== undefined) pt.timestamp = p.timestamp;
  const avg = p.avgValue ?? p.avg_value;
  if (avg !== undefined) pt.avgValue = avg;
  const min = p.minValue ?? p.min_value;
  if (min !== undefined) pt.minValue = min;
  const max = p.maxValue ?? p.max_value;
  if (max !== undefined) pt.maxValue = max;
  return pt;
}

export function normalizeSeriesData(s?: RawSeriesData): SeriesData | undefined {
  if (!s) return undefined;
  const points = (s.points ?? []).map((p) => normalizeSeriesPoint(p));
  const res: SeriesData = { points };
  const metric = s.metricName ?? s.metric_name;
  if (metric !== undefined) res.metricName = metric;
  const wu = s.workUnitId ?? s.work_unit_id;
  if (wu !== undefined) res.workUnitId = wu;
  if (s.xid !== undefined) res.xid = s.xid;
  const label = s.seriesLabel ?? s.series_label;
  if (label !== undefined) res.seriesLabel = label;
  const base = s.isBaseline ?? s.is_baseline;
  if (base !== undefined) res.isBaseline = base;
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

export function normalizePlotData(pd?: RawPlotData): PlotData | undefined {
  if (!pd) return undefined;
  const rawBar = extractRawBar(pd);
  const rawSeriesList = pd.seriesList ?? pd.series_list;
  const rawSummary = extractRawSummary(pd);
  const rawScatter = extractRawScatter(pd);

  const result: PlotData = {
    plotId: pd.plotId ?? pd.plot_id ?? '',
    type: pd.type,
  };

  const summary = normalizeSummaryBoxValue(rawSummary);
  if (summary !== undefined) {
    result.summaryData = summary;
  }

  const scatter = normalizeSeriesData(rawScatter);
  if (scatter !== undefined) {
    result.scatterSeries = scatter;
  }

  if (rawBar) {
    result.barData = {
      metricName: rawBar.metricName ?? rawBar.metric_name,
      buckets: (rawBar.buckets ?? []).map((b) => ({
        categoryOrBin: b.categoryOrBin ?? b.category_or_bin,
        valueOrCount: b.valueOrCount ?? b.value_or_count,
      })),
    };
  }

  if (rawSeriesList) {
    result.seriesList = rawSeriesList
      .map((s) => normalizeSeriesData(s))
      .filter(Boolean) as SeriesData[];
  }

  return result;
}

export function normalizeChartData(cd?: RawChartData): ChartData | undefined {
  if (!cd) return undefined;
  const rawPlots = cd.plotsData ?? cd.plots_data ?? [];
  return {
    name: cd.name,
    xid: cd.xid,
    wid: cd.wid,
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

export function normalizePlotSpec(p: RawPlotSpec): PlotSpec {
  const result: PlotSpec = {
    plotId: p.plotId ?? p.plot_id ?? '',
    type: p.type ?? PlotType.SCATTER,
    data: p.data ?? { metrics: [] },
  };
  if (p.title !== undefined) result.title = p.title;
  if (p.mapping !== undefined) result.mapping = p.mapping;
  if (p.geoms !== undefined) result.geoms = p.geoms;
  if (p.stats !== undefined) result.stats = p.stats;
  if (p.coords !== undefined) result.coords = p.coords;
  if (p.facets !== undefined) result.facets = p.facets;
  return result;
}

export function normalizeChart(c: RawChart): Chart {
  const xidNum = typeof c.xid === 'string' ? parseInt(c.xid, 10) : (c.xid ?? 0);
  const widNum = typeof c.wid === 'string' ? parseInt(c.wid, 10) : c.wid;
  const plots = (c.plots ?? []).map((p) => normalizePlotSpec(p));
  const result: Chart = {
    title: c.title ?? `Experiment ${xidNum}`,
    xid: xidNum,
    plots,
  };
  if (c.name !== undefined) result.name = c.name;
  if (c.description !== undefined) result.description = c.description;
  if (widNum !== undefined && !isNaN(widNum)) result.wid = widNum;
  const layout = c.plotLayout ?? c.plot_layout;
  if (layout !== undefined) result.plotLayout = layout;
  const createTime = c.createTime ?? c.create_time;
  if (createTime !== undefined) result.createTime = createTime;
  const updateTime = c.updateTime ?? c.update_time;
  if (updateTime !== undefined) result.updateTime = updateTime;
  return result;
}

export function normalizeDashboard(d: RawDashboard): Dashboard {
  const result: Dashboard = {
    title: d.title ?? d.name ?? 'Dashboard',
  };
  if (d.name !== undefined) result.name = d.name;
  if (d.description !== undefined) result.description = d.description;
  if (d.owner !== undefined) result.owner = d.owner;
  if (d.charts !== undefined) {
    result.charts = d.charts.map((c) => normalizeChart(c));
  }
  const chartLayout = d.chartLayout ?? d.chart_layout;
  if (chartLayout !== undefined) result.chartLayout = chartLayout;
  const createTime = d.createTime ?? d.create_time;
  if (createTime !== undefined) result.createTime = createTime;
  const updateTime = d.updateTime ?? d.update_time;
  if (updateTime !== undefined) result.updateTime = updateTime;
  return result;
}
