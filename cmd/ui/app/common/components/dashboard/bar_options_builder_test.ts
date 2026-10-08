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

import { PlotData, PlotSpec, PlotType } from 'xmanager_cloud/ui/app/service/dashboard_models';

import { buildBarOption } from './bar_options_builder';

describe('bar_options_builder', () => {
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

    it('should build bar chart with categories, values, and series name from spec title', () => {
      const option = buildBarOption(mockSpec, mockBarData);
      expect((option.xAxis as any)?.data).toEqual(['WorkUnit 1', 'WorkUnit 2', 'WorkUnit 3']);
      const seriesArr = option.series as any[];
      expect(seriesArr[0].type).toBe('bar');
      expect(seriesArr[0].name).toBe('Accuracy Distribution');
      expect(seriesArr[0].data).toEqual([0.92, 0.88, 0.94]);
    });

    it('should respect yMin and yMax bounds on yAxis when defined in spec coords', () => {
      const specWithCoords: PlotSpec = {
        ...mockSpec,
        coords: { yMin: 0, yMax: 1.0 },
      };
      const option = buildBarOption(specWithCoords, mockBarData);
      expect((option.yAxis as any)?.min).toBe(0);
      expect((option.yAxis as any)?.max).toBe(1.0);
    });

    it('should fallback series name to metricName or Value when spec title is omitted', () => {
      const specNoTitle: PlotSpec = {
        plotId: 'accuracy_dist',
        type: PlotType.BAR,
        data: { metrics: ['eval/accuracy'] },
      };
      const optionMetric = buildBarOption(specNoTitle, mockBarData);
      expect((optionMetric.series as any[])[0].name).toBe('eval/accuracy');

      const dataNoMetric: PlotData = {
        plotId: 'accuracy_dist',
        type: 'BAR',
        barData: {
          buckets: [{ categoryOrBin: 'Bin 1', valueOrCount: 5 }],
        },
      };
      const optionFallback = buildBarOption(specNoTitle, dataNoMetric);
      expect((optionFallback.series as any[])[0].name).toBe('Value');
    });
  });
});
