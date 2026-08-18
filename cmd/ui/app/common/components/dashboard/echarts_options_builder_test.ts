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

import {
  ChartData,
  GeomType,
  PlotData,
  PlotSpec,
  PlotType,
} from 'xmanager_cloud/ui/app/service/dashboard_models';

import {
  buildBarOption,
  buildEChartsOption,
  buildScatterOption,
  getEmptyChartOption,
} from './echarts_options_builder';
import { XMC_BASELINE_COLOR } from './echarts_theme';

describe('echarts_options_builder', () => {
  describe('getEmptyChartOption', () => {
    it('should return fallback message when data is empty or missing', () => {
      const option = getEmptyChartOption();
      expect((option.title as any)?.text).toBe('No metric data available');
      expect((option.series as any[])?.length).toBe(0);
    });
  });

  describe('Golden dataset: scatter_identity_raw', () => {
    // Derived from test/dataengine/testdata/golden/scatter_identity_raw.golden.json
    const mockPlotData: PlotData = {
      plotId: 'raw_loss_plot',
      type: 'SCATTER',
      scatterSeries: {
        metricName: 'eval/loss',
        workUnitId: '1',
        xid: '9001',
        points: [
          { step: '0', avgValue: 1.85, minValue: 1.85, maxValue: 1.85 },
          { step: '100', avgValue: 1.1404, minValue: 1.1404, maxValue: 1.1404 },
          { step: '200', avgValue: 0.7105, minValue: 0.7105, maxValue: 0.7105 },
          { step: '300', avgValue: 0.4439, minValue: 0.4439, maxValue: 0.4439 },
          { step: '400', avgValue: 0.2789, minValue: 0.2789, maxValue: 0.2789 },
          { step: '500', avgValue: 0.1631, minValue: 0.1631, maxValue: 0.1631 },
        ],
      },
    };

    const mockSpec: PlotSpec = {
      plotId: 'raw_loss_plot',
      title: 'Raw Loss',
      type: PlotType.SCATTER,
      data: {
        metrics: ['eval/loss'],
      },
      coords: {
        yMin: 0,
        yMax: 2.0,
      },
    };

    it('should build line curve with coordinate data from golden points', () => {
      const option = buildEChartsOption(mockSpec, mockPlotData);
      expect(option.series).toBeDefined();
      const seriesArr = option.series as any[];
      expect(seriesArr.length).toBe(1);
      expect(seriesArr[0].name).toBe('eval/loss');
      expect(seriesArr[0].data).toEqual([
        [0, 1.85],
        [100, 1.1404],
        [200, 0.7105],
        [300, 0.4439],
        [400, 0.2789],
        [500, 0.1631],
      ]);
      expect((option.yAxis as any)?.min).toBe(0);
      expect((option.yAxis as any)?.max).toBe(2.0);
    });
  });

  describe('Golden dataset: scatter_downsampled_buckets', () => {
    // Derived from test/dataengine/testdata/golden/scatter_downsampled_buckets.golden.json
    const mockPlotData: PlotData = {
      plotId: 'downsampled_plot',
      type: 'SCATTER',
      scatterSeries: {
        metricName: 'downsample_metric',
        workUnitId: '1',
        xid: '9001',
        points: [
          { step: '0', avgValue: 0.9998, minValue: 0.99, maxValue: 1.0 },
          { step: '100', avgValue: 0.985, minValue: 0.98, maxValue: 0.99 },
          { step: '200', avgValue: 0.965, minValue: 0.95, maxValue: 0.97 },
          { step: '300', avgValue: 0.932, minValue: 0.92, maxValue: 0.94 },
        ],
      },
    };

    const mockSpec: PlotSpec = {
      plotId: 'downsampled_plot',
      title: 'Downsampled Metric',
      type: PlotType.SCATTER,
      data: {
        metrics: ['downsample_metric'],
      },
    };

    it('should correctly map downsampled bucket step numbers', () => {
      const option = buildScatterOption(mockSpec, mockPlotData);
      const seriesArr = option.series as any[];
      expect(seriesArr.length).toBe(1);
      expect(seriesArr[0].data).toEqual([
        [0, 0.9998],
        [100, 0.985],
        [200, 0.965],
        [300, 0.932],
      ]);
    });
  });

  describe('Golden dataset: scatter_multi_series & baseline overlay', () => {
    // Multi-run overlay per Figma requirements
    const mockPlotData: PlotData = {
      plotId: 'multi_series_plot',
      type: 'SCATTER',
      seriesList: [
        {
          metricName: 'loss/train',
          seriesLabel: 'Run 1',
          points: [
            { step: 0, avgValue: 0.9 },
            { step: 10, avgValue: 0.6 },
            { step: 20, avgValue: 0.4 },
          ],
        },
        {
          metricName: 'loss/train',
          seriesLabel: 'Run 2',
          points: [
            { step: 0, avgValue: 0.85 },
            { step: 10, avgValue: 0.55 },
            { step: 20, avgValue: 0.38 },
          ],
        },
        {
          metricName: 'loss/train',
          seriesLabel: 'Baseline',
          isBaseline: true,
          points: [
            { step: 0, avgValue: 0.7 },
            { step: 10, avgValue: 0.7 },
            { step: 20, avgValue: 0.7 },
          ],
        },
      ],
    };

    const mockSpec: PlotSpec = {
      plotId: 'multi_series_plot',
      title: 'Validation loss vs steps',
      type: PlotType.SCATTER,
      data: { metrics: ['loss/train'] },
      geoms: [
        {
          type: GeomType.HLINE,
          yintercept: 0.7,
        },
      ],
    };

    it('should generate multiple series with distinct colors and dotted baseline', () => {
      const option = buildScatterOption(mockSpec, mockPlotData);
      const seriesArr = option.series as any[];
      expect(seriesArr.length).toBe(4); // 3 data series + 1 markLine baseline geom

      expect(seriesArr[0].name).toBe('Run 1');
      expect(seriesArr[1].name).toBe('Run 2');
      expect(seriesArr[2].name).toBe('Baseline');
      expect(seriesArr[2].lineStyle.type).toBe('dotted');
      expect(seriesArr[2].lineStyle.color).toBe(XMC_BASELINE_COLOR);

      // Verify markLine baseline
      expect(seriesArr[3].markLine.data[0].yAxis).toBe(0.7);
    });

    it('should ignore geoms with null or undefined yintercept', () => {
      const specWithNullGeom: PlotSpec = {
        ...mockSpec,
        geoms: [
          { type: GeomType.HLINE, yintercept: null as unknown as number },
          { type: GeomType.HLINE, yintercept: undefined },
        ],
      };
      const option = buildScatterOption(specWithNullGeom, mockPlotData);
      const seriesArr = option.series as any[];
      expect(seriesArr.length).toBe(3); // Only 3 data series, no markLine series
    });
  });

  describe('Golden dataset: bar_data', () => {
    const mockBarData: PlotData = {
      plotId: 'accuracy_dist',
      type: 'BAR',
      barData: {
        metricName: 'eval/accuracy',
        buckets: [
          { categoryOrBin: 'WorkUnit 1', valueOrCount: 0.92 },
          { categoryOrBin: 'WorkUnit 2', valueOrCount: 0.88 },
          { categoryOrBin: 'WorkUnit 3', valueOrCount: 0.94 },
        ],
      },
    };

    const mockSpec: PlotSpec = {
      plotId: 'accuracy_dist',
      title: 'Accuracy Distribution',
      type: PlotType.BAR,
      data: { metrics: ['eval/accuracy'] },
    };

    it('should build bar chart with categories and values', () => {
      const option = buildBarOption(mockSpec, mockBarData);
      expect((option.xAxis as any)?.data).toEqual(['WorkUnit 1', 'WorkUnit 2', 'WorkUnit 3']);
      const seriesArr = option.series as any[];
      expect(seriesArr[0].type).toBe('bar');
      expect(seriesArr[0].data).toEqual([0.92, 0.88, 0.94]);
    });
  });

  describe('buildEChartsOption type resolution', () => {
    const mockScatterData: PlotData = {
      plotId: 'p1',
      scatterSeries: {
        metricName: 'loss',
        points: [{ step: 0, avgValue: 1.0 }],
      },
    };

    const mockBarData: PlotData = {
      plotId: 'p2',
      barData: {
        metricName: 'count',
        buckets: [{ categoryOrBin: 'bin1', valueOrCount: 10 }],
      },
    };

    const unspecifiedSpec: PlotSpec = {
      plotId: 'p_unspec',
      type: PlotType.PLOT_TYPE_UNSPECIFIED,
      data: { metrics: ['loss'] },
    };

    it('should resolve scatter option when spec type is unspecified and data type is numeric enum, number, or string', () => {
      const typesToTest: Array<PlotType | string | number> = [PlotType.SCATTER, 2, 'SCATTER'];
      for (const t of typesToTest) {
        const option = buildEChartsOption(unspecifiedSpec, { ...mockScatterData, type: t });
        expect(option.series).toBeDefined();
        const seriesArr = option.series as any[];
        expect(seriesArr.length).toBe(1);
        expect(seriesArr[0].name).toBe('loss');
      }
    });

    it('should resolve bar option when spec type is unspecified and data type is numeric enum, number, or string', () => {
      const typesToTest: Array<PlotType | string | number> = [
        PlotType.BAR,
        3,
        PlotType.HISTOGRAM,
        4,
        'BAR',
        'HISTOGRAM',
      ];
      for (const t of typesToTest) {
        const option = buildEChartsOption(unspecifiedSpec, { ...mockBarData, type: t });
        expect(option.series).toBeDefined();
        const seriesArr = option.series as any[];
        expect(seriesArr.length).toBe(1);
        expect(seriesArr[0].type).toBe('bar');
      }
    });

    it('should resolve bar option when spec type is HISTOGRAM', () => {
      const histogramSpec: PlotSpec = {
        plotId: 'p_hist',
        type: PlotType.HISTOGRAM,
        data: { metrics: ['count'] },
      };
      const option = buildEChartsOption(histogramSpec, mockBarData);
      expect(option.series).toBeDefined();
      const seriesArr = option.series as any[];
      expect(seriesArr.length).toBe(1);
      expect(seriesArr[0].type).toBe('bar');
    });
  });
});
