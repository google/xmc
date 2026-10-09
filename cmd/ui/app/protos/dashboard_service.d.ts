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

export type PlotType =
  | 'PLOT_TYPE_UNSPECIFIED'
  | 'SUMMARY_BOX'
  | 'SCATTER'
  | 'BAR'
  | 'HISTOGRAM'
  | number;

export type LayoutType =
  | 'LAYOUT_TYPE_UNSPECIFIED'
  | 'GRID'
  | 'WRAP'
  | 'SIDE_BY_SIDE'
  | 'STACKED'
  | number;

export type GeomType =
  | 'GEOM_TYPE_UNSPECIFIED'
  | 'LINE'
  | 'POINT'
  | 'BAR'
  | 'AREA'
  | 'RIBBON'
  | 'HLINE'
  | 'VLINE'
  | number;

export interface GridPosition {
  itemId?: string;
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  [key: string]: any;
}

export namespace LayoutSpec {
  export { type LayoutType };
}

export interface LayoutSpec {
  type?: LayoutSpec.LayoutType | LayoutType | number;
  columns?: number;
  positions?: GridPosition[];
  [key: string]: any;
}

export namespace DataSpec {
  export interface DataRange {
    startStep?: number | string;
    startTime?: string;
    endStep?: number | string;
    endTime?: string;
    [key: string]: any;
  }
}

export interface DataSpec {
  metrics?: string[];
  ranges?: DataSpec.DataRange[];
  [key: string]: any;
}

export interface MappingSpec {
  x?: string;
  y?: string;
  color?: string;
  group?: string;
  size?: string;
  [key: string]: any;
}

export namespace GeomSpec {
  export { type GeomType };

  export interface StyleSpec {
    color?: string;
    opacity?: number;
    size?: number;
    lineStyle?: string;
    [key: string]: any;
  }
}

export interface GeomSpec {
  type?: GeomSpec.GeomType | GeomType | number;
  yintercept?: number;
  xintercept?: number;
  style?: GeomSpec.StyleSpec;
  [key: string]: any;
}

export namespace StatSpec {
  export type StatType =
    | 'STAT_TYPE_UNSPECIFIED'
    | 'IDENTITY'
    | 'SUMMARY'
    | 'DOWNSAMPLE'
    | 'SMOOTH'
    | 'FORECAST'
    | number;

  export type Aggregation =
    | 'AGG_UNSPECIFIED'
    | 'MEAN'
    | 'MEDIAN'
    | 'MIN'
    | 'MAX'
    | 'SUM'
    | 'MEAN_CI_95'
    | number;

  export namespace SmoothConfig {
    export type Algorithm = 'EXPONENTIAL' | 'MOVING_AVERAGE' | number;
  }

  export interface SmoothConfig {
    algorithm?: SmoothConfig.Algorithm | number;
    alpha?: number;
    windowSize?: number;
    [key: string]: any;
  }

  export interface ForecastConfig {
    alpha?: number;
    beta?: number;
    stepsAhead?: number;
    [key: string]: any;
  }
}

export interface StatSpec {
  type?: StatSpec.StatType | number;
  aggregation?: StatSpec.Aggregation | number;
  smooth?: StatSpec.SmoothConfig;
  forecast?: StatSpec.ForecastConfig;
  [key: string]: any;
}

export namespace CoordSpec {
  export type ScaleType = 'SCALE_TYPE_UNSPECIFIED' | 'LINEAR' | 'LOG' | 'SYMLOG' | number;
}

export interface CoordSpec {
  xScale?: CoordSpec.ScaleType | number;
  yScale?: CoordSpec.ScaleType | number;
  yMin?: number;
  yMax?: number;
  [key: string]: any;
}

export namespace FacetSpec {
  export type FacetType = 'FACET_TYPE_UNSPECIFIED' | 'WRAP' | 'GRID' | number;
}

export interface FacetSpec {
  type?: FacetSpec.FacetType | number;
  facetBy?: string[];
  ncol?: number;
  nrow?: number;
  [key: string]: any;
}

export interface PlotSpec {
  plotId?: string;
  title?: string;
  type?: PlotType | number;
  data?: DataSpec;
  mapping?: MappingSpec;
  geoms?: GeomSpec[];
  stats?: StatSpec;
  coords?: CoordSpec;
  facets?: FacetSpec;
  [key: string]: any;
}

export interface Chart {
  name?: string;
  title?: string;
  description?: string;
  xid?: number | string;
  wid?: number | string;
  plots?: PlotSpec[];
  plotLayout?: LayoutSpec;
  plot_layout?: LayoutSpec;
  createTime?: string;
  updateTime?: string;
  [key: string]: any;
}

export interface Dashboard {
  name?: string;
  title?: string;
  description?: string;
  owner?: string;
  chartIds?: string[];
  chart_ids?: string[];
  chartLayout?: LayoutSpec;
  chart_layout?: LayoutSpec;
  createTime?: string;
  updateTime?: string;
  [key: string]: any;
}

export namespace ChartData {
  export interface SummaryBoxValue {
    metricName?: string;
    latestValue?: number;
    minValue?: number;
    maxValue?: number;
    meanValue?: number;
    latestStep?: number | string;
    latestTimestamp?: string;
    [key: string]: any;
  }

  export interface SeriesPoint {
    step?: number | string;
    timestamp?: string;
    avgValue?: number;
    minValue?: number;
    maxValue?: number;
    [key: string]: any;
  }

  export interface SeriesData {
    metricName?: string;
    workUnitId?: string;
    xid?: number | string;
    points?: SeriesPoint[];
    [key: string]: any;
  }

  export interface BarBucket {
    categoryOrBin?: string;
    valueOrCount?: number;
    [key: string]: any;
  }

  export interface BarData {
    metricName?: string;
    buckets?: BarBucket[];
    [key: string]: any;
  }

  export interface PlotData {
    plotId?: string;
    type?: PlotType | number;
    summaryData?: SummaryBoxValue;
    summary_data?: SummaryBoxValue;
    scatterSeries?: SeriesData;
    scatter_series?: SeriesData;
    barData?: BarData;
    bar_data?: BarData;
    seriesList?: SeriesData[];
    series_list?: SeriesData[];
    content?: {
      summaryData?: SummaryBoxValue;
      summary_data?: SummaryBoxValue;
      scatterSeries?: SeriesData;
      scatter_series?: SeriesData;
      barData?: BarData;
      bar_data?: BarData;
    };
    [key: string]: any;
  }
}

export interface ChartData {
  name?: string;
  xid?: number | string;
  wid?: number | string;
  plotsData?: ChartData.PlotData[];
  plots_data?: ChartData.PlotData[];
  [key: string]: any;
}

export interface CreateDashboardRequest {
  dashboard?: any;
  dashboardId?: string;
  charts?: any[];
  [key: string]: any;
}

export interface GetDashboardRequest {
  name?: string;
  [key: string]: any;
}

export interface ListDashboardsRequest {
  pageSize?: number;
  pageToken?: string;
  filter?: string;
  [key: string]: any;
}

export interface ListDashboardsResponse {
  dashboards?: any[];
  nextPageToken?: string;
  [key: string]: any;
}

export interface UpdateDashboardRequest {
  dashboard?: any;
  updateMask?: string;
  [key: string]: any;
}

export interface DeleteDashboardRequest {
  name?: string;
  [key: string]: any;
}

export interface CreateChartRequest {
  parent?: string;
  chart?: any;
  chartId?: string;
  [key: string]: any;
}

export interface GetChartRequest {
  name?: string;
  [key: string]: any;
}

export interface GetChartDataRequest {
  name?: string;
  parentExperiment?: string;
  parentWorkunit?: string;
  chartWidthPixels?: number;
  [key: string]: any;
}

export interface ListChartsRequest {
  parent?: string;
  pageSize?: number;
  pageToken?: string;
  [key: string]: any;
}

export interface ListChartsResponse {
  charts?: any[];
  nextPageToken?: string;
  [key: string]: any;
}

export interface UpdateChartRequest {
  chart?: any;
  updateMask?: string;
  [key: string]: any;
}

export interface DeleteChartRequest {
  name?: string;
  [key: string]: any;
}
