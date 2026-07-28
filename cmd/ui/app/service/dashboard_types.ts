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

/** Supported plot visualization types in a Chart. */
export enum PlotType {
  PLOT_TYPE_UNSPECIFIED = 'PLOT_TYPE_UNSPECIFIED',
  SUMMARY_BOX = 'SUMMARY_BOX',
  SCATTER = 'SCATTER',
  BAR = 'BAR',
  HISTOGRAM = 'HISTOGRAM',
}

/** Supported layout geometries for positioning charts or child plots. */
export enum LayoutType {
  LAYOUT_TYPE_UNSPECIFIED = 'LAYOUT_TYPE_UNSPECIFIED',
  GRID = 'GRID',
  WRAP = 'WRAP',
  SIDE_BY_SIDE = 'SIDE_BY_SIDE',
  STACKED = 'STACKED',
}

/** Explicit cell coordinate positioning for a GRID layout. */
export interface GridPosition {
  readonly itemId: string;
  readonly x: number;
  readonly y: number;
  readonly width: number;
  readonly height: number;
}

/** Layout specification defining grid or flow arrangement. */
export interface LayoutSpec {
  readonly type: LayoutType;
  readonly columns?: number;
  readonly positions?: readonly GridPosition[];
}

/** Specification for an individual plot inside a Chart. */
export interface PlotSpec {
  readonly plotId: string;
  readonly title?: string;
  readonly type: PlotType;
  readonly data: {
    readonly metrics: readonly string[];
    readonly sources: readonly {
      readonly xid: number;
      readonly workUnitId?: string;
    }[];
  };
}

/** Experiment-scoped Chart resource. */
export interface Chart {
  readonly name: string;
  readonly title: string;
  readonly description?: string;
  readonly xid: number;
  readonly plots: readonly PlotSpec[];
  readonly plotLayout: LayoutSpec;
}

/** Multi-experiment Dashboard resource. */
export interface Dashboard {
  readonly name: string;
  readonly title: string;
  readonly description?: string;
  readonly owner?: string;
  readonly charts: readonly Chart[];
  readonly chartLayout: LayoutSpec;
}

/** Request payload for GetChartData custom analytics RPC. */
export interface GetChartDataRequest {
  readonly name: string;
  readonly plotIds?: readonly string[];
  readonly startIteration?: number;
  readonly endIteration?: number;
  readonly startTime?: string;
  readonly endTime?: string;
  readonly chartWidthPixels?: number;
}

/** KPI scalar metrics summary for SUMMARY_BOX plot. */
export interface SummaryBoxValue {
  readonly metricName: string;
  readonly latestValue: number;
  readonly minValue: number;
  readonly maxValue: number;
  readonly meanValue: number;
  readonly latestIteration: number;
  readonly latestTimestamp?: string;
}

/** A single iteration point for time-series / scatter plots. */
export interface SeriesPoint {
  readonly iteration: number;
  readonly timestamp?: string;
  readonly avgValue: number;
  readonly minValue: number;
  readonly maxValue: number;
}

/** Series data trace for SCATTER plot. */
export interface SeriesData {
  readonly metricName: string;
  readonly workUnitId: string;
  readonly xid: number;
  readonly points: readonly SeriesPoint[];
}

/** A single bar bucket or category count. */
export interface BarBucket {
  readonly categoryOrBin: string;
  readonly valueOrCount: number;
}

/** Categorical bar chart data trace for BAR plot. */
export interface BarData {
  readonly metricName: string;
  readonly buckets: readonly BarBucket[];
}

/** Oneof data payload for an individual plot inside ChartData. */
export interface PlotData {
  readonly plotId: string;
  readonly type: PlotType;
  readonly summaryData?: SummaryBoxValue;
  readonly scatterSeries?: SeriesData;
  readonly barData?: BarData;
}

/** Response payload returned directly by GetChartData. */
export interface ChartData {
  readonly name: string;
  readonly xid: number;
  readonly plotsData: readonly PlotData[];
}
