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
  item_id?: string;
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
    start_step?: number | string;
    startTime?: string;
    start_time?: string;
    endStep?: number | string;
    end_step?: number | string;
    endTime?: string;
    end_time?: string;
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
    line_style?: string;
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
    window_size?: number;
    [key: string]: any;
  }
  export interface ForecastConfig {
    alpha?: number;
    beta?: number;
    stepsAhead?: number;
    steps_ahead?: number;
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
  x_scale?: CoordSpec.ScaleType | number;
  yScale?: CoordSpec.ScaleType | number;
  y_scale?: CoordSpec.ScaleType | number;
  yMin?: number;
  y_min?: number;
  yMax?: number;
  y_max?: number;
  [key: string]: any;
}

export namespace FacetSpec {
  export type FacetType = 'FACET_TYPE_UNSPECIFIED' | 'WRAP' | 'GRID' | number;
}

export interface FacetSpec {
  type?: FacetSpec.FacetType | number;
  facetBy?: string[];
  facet_by?: string[];
  ncol?: number;
  nrow?: number;
  [key: string]: any;
}

export interface PlotSpec {
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
  create_time?: string;
  updateTime?: string;
  update_time?: string;
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
  create_time?: string;
  updateTime?: string;
  update_time?: string;
  [key: string]: any;
}

export namespace ChartData {
  export interface SummaryBoxValue {
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
    polarity?: 'LOWER_IS_BETTER' | 'HIGHER_IS_BETTER' | 'NEUTRAL';
    [key: string]: any;
  }

  export interface SeriesPoint {
    step?: number | string;
    iteration?: number | string;
    timestamp?: string;
    avgValue?: number;
    avg_value?: number;
    minValue?: number;
    min_value?: number;
    maxValue?: number;
    max_value?: number;
    [key: string]: any;
  }

  export interface SeriesData {
    metricName?: string;
    metric_name?: string;
    workUnitId?: string;
    work_unit_id?: string;
    xid?: number | string;
    points?: SeriesPoint[];
    seriesLabel?: string;
    series_label?: string;
    isBaseline?: boolean;
    is_baseline?: boolean;
    [key: string]: any;
  }

  export interface BarBucket {
    categoryOrBin?: string;
    category_or_bin?: string;
    valueOrCount?: number;
    value_or_count?: number;
    [key: string]: any;
  }

  export interface BarData {
    metricName?: string;
    metric_name?: string;
    buckets?: BarBucket[];
    [key: string]: any;
  }

  export interface PlotData {
    plotId?: string;
    plot_id?: string;
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

export type CreateDashboardRequest = Record<string, any> & {
  dashboard?: Dashboard;
  dashboardId?: string;
  charts?: Chart[];
};
export type GetDashboardRequest = Record<string, any> & { name?: string };
export type ListDashboardsRequest = Record<string, any> & {
  pageSize?: number;
  pageToken?: string;
  filter?: string;
};
export type ListDashboardsResponse = Record<string, any> & {
  dashboards?: Dashboard[];
  nextPageToken?: string;
};
export type UpdateDashboardRequest = Record<string, any> & {
  dashboard?: Dashboard;
  updateMask?: string;
};
export type DeleteDashboardRequest = Record<string, any> & { name?: string };
export type CreateChartRequest = Record<string, any> & {
  parent?: string;
  chart?: Chart;
  chartId?: string;
};
export type GetChartRequest = Record<string, any> & { name?: string };
export type GetChartDataRequest = Record<string, any> & {
  name?: string;
  parentExperiment?: string;
  parentWorkunit?: string;
  chartWidthPixels?: number;
};
export type ListChartsRequest = Record<string, any> & {
  parent?: string;
  pageSize?: number;
  pageToken?: string;
};
export type ListChartsResponse = Record<string, any> & { charts?: Chart[]; nextPageToken?: string };
export type UpdateChartRequest = Record<string, any> & { chart?: Chart; updateMask?: string };
export type DeleteChartRequest = Record<string, any> & { name?: string };
