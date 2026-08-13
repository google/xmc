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
