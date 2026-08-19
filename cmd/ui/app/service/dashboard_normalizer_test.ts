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

import 'jasmine';

import { MetricPolarity, PlotType } from './dashboard_models';
import {
  normalizeChart,
  normalizeChartData,
  normalizeDashboard,
  normalizePlotData,
  normalizePlotSpec,
  normalizeSeriesData,
  normalizeSeriesPoint,
  normalizeSummaryBoxValue,
  parseNumericId,
} from './dashboard_normalizer';

describe('dashboard_normalizer', () => {
  describe('parseNumericId', () => {
    it('should parse valid number inputs and strings', () => {
      expect(parseNumericId(123)).toBe(123);
      expect(parseNumericId('456')).toBe(456);
      expect(parseNumericId(0)).toBe(0);
      expect(parseNumericId('0')).toBe(0);
    });

    it('should return undefined for invalid or empty inputs', () => {
      expect(parseNumericId(undefined)).toBeUndefined();
      expect(parseNumericId(null)).toBeUndefined();
      expect(parseNumericId('')).toBeUndefined();
      expect(parseNumericId('abc')).toBeUndefined();
      expect(parseNumericId(true as unknown as number)).toBeUndefined();
      expect(parseNumericId(false as unknown as number)).toBeUndefined();
      expect(parseNumericId({} as unknown as number)).toBeUndefined();
    });
  });

  describe('normalizeSummaryBoxValue', () => {
    it('should return undefined for undefined input', () => {
      expect(normalizeSummaryBoxValue(undefined)).toBeUndefined();
    });

    it('should normalize camelCase and snake_case fields correctly', () => {
      const result = normalizeSummaryBoxValue({
        metric_name: 'loss',
        latest_value: 0.12,
        min_value: 0.05,
        max_value: 1.5,
        mean_value: 0.45,
        latest_step: 100,
        latest_iteration: 2,
        latest_timestamp: '2026-08-17T00:00:00Z',
        delta_vs_baseline: -0.05,
        delta_percent_vs_baseline: -29.4,
        baseline_value: 0.17,
        polarity: MetricPolarity.LOWER_IS_BETTER,
      });

      expect(result).toBeDefined();
      expect(result?.metricName).toBe('loss');
      expect(result?.latestValue).toBe(0.12);
      expect(result?.minValue).toBe(0.05);
      expect(result?.maxValue).toBe(1.5);
      expect(result?.meanValue).toBe(0.45);
      expect(result?.latestStep).toBe(100);
      expect(result?.latestIteration).toBe(2);
      expect(result?.latestTimestamp).toBe('2026-08-17T00:00:00Z');
      expect(result?.deltaVsBaseline).toBe(-0.05);
      expect(result?.deltaPercentVsBaseline).toBe(-29.4);
      expect(result?.baselineValue).toBe(0.17);
      expect(result?.polarity).toBe(MetricPolarity.LOWER_IS_BETTER);
    });

    it('should preserve 0 values and filter null fields', () => {
      const result = normalizeSummaryBoxValue({
        metricName: 'acc',
        latestValue: 0,
        minValue: 0,
        maxValue: 0,
      });

      expect(result?.metricName).toBe('acc');
      expect(result?.latestValue).toBe(0);
      expect(result?.minValue).toBe(0);
      expect(result?.maxValue).toBe(0);
    });
  });

  describe('normalizeSeriesPoint', () => {
    it('should normalize series point fields and aliases', () => {
      const pt = normalizeSeriesPoint({
        step: 50,
        iteration: 1,
        timestamp: '2026-08-17T00:00:00Z',
        avg_value: 0.95,
        min_value: 0.9,
        max_value: 0.98,
      });

      expect(pt.step).toBe(50);
      expect(pt.iteration).toBe(1);
      expect(pt.timestamp).toBe('2026-08-17T00:00:00Z');
      expect(pt.avgValue).toBe(0.95);
      expect(pt.minValue).toBe(0.9);
      expect(pt.maxValue).toBe(0.98);
    });
  });

  describe('normalizeSeriesData', () => {
    it('should return undefined for undefined input', () => {
      expect(normalizeSeriesData(undefined)).toBeUndefined();
    });

    it('should normalize series points and identifiers', () => {
      const series = normalizeSeriesData({
        metric_name: 'eval_loss',
        work_unit_id: 'wu_1',
        xid: '234911122',
        series_label: 'Baseline Run',
        is_baseline: true,
        points: [{ step: 1, avg_value: 2.5 }],
      });

      expect(series).toBeDefined();
      expect(series?.metricName).toBe('eval_loss');
      expect(series?.workUnitId).toBe('wu_1');
      expect(series?.xid).toBe(234911122);
      expect(series?.seriesLabel).toBe('Baseline Run');
      expect(series?.isBaseline).toBeTrue();
      expect(series?.points?.length).toBe(1);
    });
  });

  describe('normalizePlotData', () => {
    it('should return undefined for undefined input', () => {
      expect(normalizePlotData(undefined)).toBeUndefined();
    });

    it('should normalize plot data with nested summary and series list', () => {
      const pd = normalizePlotData({
        plot_id: 'loss_curve',
        type: PlotType.SCATTER,
        content: {
          summary_data: { metric_name: 'loss', latest_value: 0.5 },
        },
        series_list: [{ metric_name: 'loss', points: [{ step: 10, avg_value: 0.5 }] }],
      });

      expect(pd).toBeDefined();
      expect(pd?.plotId).toBe('loss_curve');
      expect(pd?.type).toBe(PlotType.SCATTER);
      expect(pd?.summaryData?.metricName).toBe('loss');
      expect(pd?.seriesList?.length).toBe(1);
    });

    it('should normalize bar data correctly', () => {
      const pd = normalizePlotData({
        plot_id: 'cat_bar',
        type: PlotType.BAR,
        content: {
          bar_data: {
            metric_name: 'latency',
            buckets: [{ category_or_bin: 'us-central1', value_or_count: 45 }],
          },
        },
      });

      expect(pd?.barData?.metricName).toBe('latency');
      expect(pd?.barData?.buckets?.[0].categoryOrBin).toBe('us-central1');
      expect(pd?.barData?.buckets?.[0].valueOrCount).toBe(45);
    });
  });

  describe('normalizeChartData', () => {
    it('should return undefined for undefined input', () => {
      expect(normalizeChartData(undefined)).toBeUndefined();
    });

    it('should normalize chart plotsData and numeric xid/wid', () => {
      const cd = normalizeChartData({
        name: 'experiments/123/charts/main',
        xid: '123',
        wid: '1',
        plots_data: [{ plot_id: 'plot_1', type: PlotType.SCATTER }],
      });

      expect(cd?.name).toBe('experiments/123/charts/main');
      expect(cd?.xid).toBe(123);
      expect(cd?.wid).toBe(1);
      expect(cd?.plotsData?.length).toBe(1);
    });
  });

  describe('normalizePlotSpec', () => {
    it('should normalize plot specification with defaults', () => {
      const spec = normalizePlotSpec({
        plot_id: 'spec_1',
        title: 'Loss Plot',
      });

      expect(spec.plotId).toBe('spec_1');
      expect(spec.title).toBe('Loss Plot');
      expect(spec.type).toBe(PlotType.SCATTER);
      expect(spec.data?.metrics).toEqual([]);
    });

    it('should defensively ensure metrics array when data is an empty object', () => {
      const spec = normalizePlotSpec({
        plot_id: 'spec_empty_data',
        data: {} as unknown as any,
      });

      expect(spec.data?.metrics).toEqual([]);
    });

    it('should return safe default PlotSpec when undefined is passed', () => {
      const spec = normalizePlotSpec(undefined);
      expect(spec.plotId).toBe('');
      expect(spec.type).toBe(PlotType.SCATTER);
      expect(spec.data?.metrics).toEqual([]);
    });
  });

  describe('normalizeChart', () => {
    it('should normalize chart specification with numeric xid and wid', () => {
      const chart = normalizeChart({
        name: 'dashboards/vcc/charts/1',
        title: 'VCC Comparison',
        xid: '234911122',
        wid: '1',
        create_time: '2026-08-17T00:00:00Z',
        plots: [{ plot_id: 'p1' }],
      });

      expect(chart.name).toBe('dashboards/vcc/charts/1');
      expect(chart.title).toBe('VCC Comparison');
      expect(chart.xid).toBe(234911122);
      expect(chart.wid).toBe(1);
      expect(chart.createTime).toBe('2026-08-17T00:00:00Z');
      expect(chart.plots?.length).toBe(1);
    });

    it('should fallback title if not provided', () => {
      const chart = normalizeChart({ xid: 99 });
      expect(chart.title).toBe('Experiment 99');
      expect(chart.xid).toBe(99);
    });

    it('should return safe default Chart when undefined is passed', () => {
      const chart = normalizeChart(undefined);
      expect(chart.title).toBe('Experiment 0');
      expect(chart.xid).toBe(0);
      expect(chart.plots).toEqual([]);
    });
  });

  describe('normalizeDashboard', () => {
    it('should normalize dashboard with charts array', () => {
      const dash = normalizeDashboard({
        name: 'dashboards/resnet',
        title: 'ResNet Benchmark',
        owner: 'user@google.com',
        charts: [{ xid: 101, title: 'Chart 101' }],
        create_time: '2026-08-17T00:00:00Z',
      });

      expect(dash.name).toBe('dashboards/resnet');
      expect(dash.title).toBe('ResNet Benchmark');
      expect(dash.owner).toBe('user@google.com');
      expect(dash.charts?.length).toBe(1);
      expect(dash.charts?.[0].title).toBe('Chart 101');
      expect(dash.createTime).toBe('2026-08-17T00:00:00Z');
    });

    it('should return safe default Dashboard when undefined is passed', () => {
      const dash = normalizeDashboard(undefined);
      expect(dash.title).toBe('Dashboard');
    });
  });
});
