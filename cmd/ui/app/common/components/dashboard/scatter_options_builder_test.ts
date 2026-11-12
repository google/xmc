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

import type { LineSeriesOption, YAXisComponentOption } from 'echarts';

import { GeomType, PlotData, PlotSpec, PlotType } from 'app/service/dashboard_models';

import { XMC_BASELINE_COLOR } from './echarts_theme';
import { buildScatterOption, extractHlineSeries } from './scatter_options_builder';

describe('scatter_options_builder', () => {
  describe('Golden dataset: scatter_identity_raw', () => {
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
      data: { metrics: ['eval/loss'] },
      coords: { yMin: 0, yMax: 2.0 },
    };

    it('should build line curve with coordinate data from golden points', () => {
      const option = buildScatterOption(mockSpec, mockPlotData);
      expect(option.series).toBeDefined();
      const seriesArr = option.series as LineSeriesOption[];
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
      expect((option.yAxis as YAXisComponentOption)?.min).toBe(0);
      expect((option.yAxis as YAXisComponentOption)?.max).toBe(2.0);
    });
  });

  describe('Golden dataset: scatter_downsampled_buckets', () => {
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
      data: { metrics: ['downsample_metric'] },
    };

    it('should correctly map downsampled bucket step numbers', () => {
      const option = buildScatterOption(mockSpec, mockPlotData);
      const seriesArr = option.series as LineSeriesOption[];
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
      geoms: [{ type: GeomType.HLINE, yintercept: 0.7 }],
    };

    it('should generate multiple series with distinct colors and dotted baseline', () => {
      const option = buildScatterOption(mockSpec, mockPlotData);
      const seriesArr = option.series as LineSeriesOption[];
      expect(seriesArr.length).toBe(4); // 3 data series + 1 markLine baseline geom

      expect(seriesArr[0].name).toBe('Run 1');
      expect(seriesArr[1].name).toBe('Run 2');
      expect(seriesArr[2].name).toBe('Baseline');
      expect(seriesArr[2].lineStyle?.type).toBe('dotted');
      expect(seriesArr[2].lineStyle?.color).toBe(XMC_BASELINE_COLOR);

      // Verify markLine baseline
      const markLineData = seriesArr[3].markLine?.data as Array<{ yAxis: number }>;
      expect(markLineData[0].yAxis).toBe(0.7);
    });

    it('should extract valid HLINE series via extractHlineSeries helper', () => {
      const hlines = extractHlineSeries(mockSpec.geoms);
      expect(hlines.length).toBe(1);
      const markLineData = (hlines[0] as LineSeriesOption).markLine?.data as Array<{
        yAxis: number;
      }>;
      expect(markLineData[0].yAxis).toBe(0.7);
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
      const seriesArr = option.series as LineSeriesOption[];
      expect(seriesArr.length).toBe(3); // Only 3 data series, no markLine series
    });
  });

  describe('getPointStep coordinate mapping', () => {
    const baseSpec: PlotSpec = {
      plotId: 'time_series_plot',
      title: 'Time Series Metric',
      type: PlotType.SCATTER,
      data: { metrics: ['system/gpu_util'] },
    };

    it('should map coordinates using timestamp when step and iteration are absent', () => {
      const timestamp1 = '2026-08-18T12:00:00Z';
      const timestamp2 = '2026-08-18T12:05:00Z';
      const parsedTime1 = Date.parse(timestamp1);
      const parsedTime2 = Date.parse(timestamp2);

      const plotData: PlotData = {
        plotId: 'time_series_plot',
        type: 'SCATTER',
        scatterSeries: {
          metricName: 'system/gpu_util',
          points: [
            { timestamp: timestamp1, avgValue: 85.5 },
            { timestamp: timestamp2, avgValue: 92.0 },
          ],
        },
      };

      const option = buildScatterOption(baseSpec, plotData);
      const seriesArr = option.series as LineSeriesOption[];
      expect(seriesArr.length).toBe(1);
      expect(seriesArr[0].data).toEqual([
        [parsedTime1, 85.5],
        [parsedTime2, 92.0],
      ]);
    });

    it('should map coordinates using numeric or string iteration when step is absent', () => {
      const plotData: PlotData = {
        plotId: 'iteration_plot',
        type: 'SCATTER',
        scatterSeries: {
          metricName: 'epoch/accuracy',
          points: [
            { iteration: 1, avgValue: 0.75 },
            { iteration: '2', avgValue: 0.82 },
          ],
        },
      };

      const option = buildScatterOption(baseSpec, plotData);
      const seriesArr = option.series as LineSeriesOption[];
      expect(seriesArr.length).toBe(1);
      expect(seriesArr[0].data).toEqual([
        [1, 0.75],
        [2, 0.82],
      ]);
    });

    it('should fallback to 0 when step or iteration is NaN or timestamp is invalid', () => {
      const plotData: PlotData = {
        plotId: 'invalid_coords_plot',
        type: 'SCATTER',
        scatterSeries: {
          metricName: 'invalid/metric',
          points: [
            { step: 'invalid_step', avgValue: 10 },
            { iteration: 'invalid_iter', avgValue: 20 },
            { timestamp: 'invalid_date', avgValue: 30 },
            { avgValue: 40 },
          ],
        },
      };

      const option = buildScatterOption(baseSpec, plotData);
      const seriesArr = option.series as LineSeriesOption[];
      expect(seriesArr.length).toBe(1);
      expect(seriesArr[0].data).toEqual([
        [0, 10],
        [0, 20],
        [0, 30],
        [0, 40],
      ]);
    });
  });
});
