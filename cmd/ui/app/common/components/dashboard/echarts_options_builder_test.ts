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

import type { SeriesOption, TitleComponentOption, XAXisComponentOption } from 'echarts';

import { PlotData, PlotSpec, PlotType } from 'app/service/dashboard_models';

import { buildEChartsOption, getEmptyChartOption } from './echarts_options_builder';
import { DEFAULT_XMC_THEME_CONFIG, XmcChartThemeConfig } from './echarts_theme';

describe('echarts_options_builder dispatcher', () => {
  describe('getEmptyChartOption', () => {
    it('should return fallback message when data is empty or missing', () => {
      const option = getEmptyChartOption();
      expect((option.title as TitleComponentOption)?.text).toBe('No metric data available');
      expect((option.series as SeriesOption[])?.length).toBe(0);
    });
  });

  describe('buildEChartsOption type resolution and dispatching', () => {
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

    it('should dispatch to scatter builder when spec type is SCATTER', () => {
      const scatterSpec: PlotSpec = {
        plotId: 'p_scatter',
        type: PlotType.SCATTER,
        data: { metrics: ['loss'] },
      };
      const option = buildEChartsOption(scatterSpec, mockScatterData);
      expect(option.series).toBeDefined();
      const seriesArr = option.series as SeriesOption[];
      expect(seriesArr.length).toBe(1);
      expect(seriesArr[0].name).toBe('loss');
    });

    it('should resolve scatter option when spec type is unspecified and data type is numeric enum, number, or string', () => {
      const typesToTest: Array<PlotType | string | number> = [PlotType.SCATTER, 2, 'SCATTER'];
      for (const t of typesToTest) {
        const option = buildEChartsOption(unspecifiedSpec, { ...mockScatterData, type: t });
        expect(option.series).toBeDefined();
        const seriesArr = option.series as SeriesOption[];
        expect(seriesArr.length).toBe(1);
        expect(seriesArr[0].name).toBe('loss');
      }
    });

    it('should dispatch to bar builder when spec type is BAR or HISTOGRAM', () => {
      const barSpec: PlotSpec = {
        plotId: 'p_bar',
        type: PlotType.BAR,
        data: { metrics: ['count'] },
      };
      const option = buildEChartsOption(barSpec, mockBarData);
      expect(option.series).toBeDefined();
      const seriesArr = option.series as SeriesOption[];
      expect(seriesArr.length).toBe(1);
      expect(seriesArr[0].type).toBe('bar');
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
        const seriesArr = option.series as SeriesOption[];
        expect(seriesArr.length).toBe(1);
        expect(seriesArr[0].type).toBe('bar');
      }
    });

    it('should apply custom theme configuration when passed', () => {
      const customTheme: XmcChartThemeConfig = {
        ...DEFAULT_XMC_THEME_CONFIG,
        axisTextColor: '#ff0000',
      };
      const scatterSpec: PlotSpec = {
        plotId: 'p_themed',
        type: PlotType.SCATTER,
        data: { metrics: ['loss'] },
      };
      const option = buildEChartsOption(scatterSpec, mockScatterData, customTheme);
      expect((option.xAxis as XAXisComponentOption)?.axisLabel?.color).toBe('#ff0000');
    });
  });
});
