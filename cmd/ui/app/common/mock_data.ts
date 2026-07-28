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

export const TOTAL_STATUS_MESSAGES: any[] = [];
export const MOCK_HISTORICAL_STATUS_MESSAGES: any[] = [
  {
    name: 'experiments/1/statusMessages/1',
    createTime: '2026-06-08T00:00:00Z',
    content: {
      userFacingText: 'Application is being initialized.',
      severity: 'INFO',
      source: 'APPLICATION',
    },
  },
  {
    name: 'experiments/1/statusMessages/2',
    createTime: '2026-06-08T00:01:00Z',
    content: {
      userFacingText: 'Critical failure: connection lost.',
      severity: 'ERROR',
      source: 'APPLICATION',
    },
  },
];

export const EXPERIMENT_1: any = {
  name: 'experiments/1',
  title: 'Experiment 1',
  notes: 'Notes for experiment 1',
  tags: ['tag1', 'tag2'],
  priority: 'LOW',
  createTime: '2026-06-08T00:00:00Z',
};

export const EXPERIMENT_2: any = {
  name: 'experiments/2',
  title: 'Experiment 2',
  notes: 'Notes for experiment 2',
  tags: ['tag3'],
  priority: 'NORMAL',
  createTime: '2026-06-08T01:00:00Z',
};

export const EXPERIMENT_LIST: any[] = [EXPERIMENT_1, EXPERIMENT_2];

export const WORK_UNIT_1: any = {
  name: 'experiments/1/workUnits/1',
  createTime: '2026-06-08T00:00:00Z',
  currentWorkUnitExecution: {
    metadata: {
      managementCluster: {
        project: 'test-project',
        location: 'us-central1',
        clusterName: 'test-cluster',
      },
      resourceName: 'test-resource',
      namespace: 'test-namespace',
      serviceAccount: 'test-sa',
    },
  },
};

export const WORK_UNIT_2: any = {
  name: 'experiments/1/workUnits/2',
  // Missing metadata for testing undefined URL resolution
};

export const WORK_UNIT_3: any = {
  name: 'experiments/1/workUnits/3',
};

export const WORK_UNIT_4: any = {
  name: 'experiments/1/workUnits/4',
};

export const WORK_UNIT_LIST: any[] = [WORK_UNIT_1, WORK_UNIT_2, WORK_UNIT_3, WORK_UNIT_4];

export const ARTIFACT_LIST: any[] = [];
export const WORK_UNIT_1_ARTIFACT_LIST: any[] = [];

export const MOCK_CHART_LOSS: any = {
  name: 'experiments/1/charts/chart-loss-perf',
  title: 'Training & Validation Loss Convergence',
  xid: 1,
  plots: [
    {
      plotId: 'plot_summary_loss',
      title: 'Loss Summary',
      type: 'SUMMARY_BOX',
      data: { metrics: ['loss/train'], sources: [{ xid: 1 }] },
    },
    {
      plotId: 'plot_curve_loss',
      title: 'Loss Curves over Iterations',
      type: 'SCATTER',
      data: { metrics: ['loss/train', 'loss/val'], sources: [{ xid: 1 }] },
    },
  ],
  plotLayout: {
    type: 'GRID',
    positions: [
      { itemId: 'plot_summary_loss', x: 0, y: 0, width: 12, height: 2 },
      { itemId: 'plot_curve_loss', x: 0, y: 2, width: 12, height: 4 },
    ],
  },
};

export const MOCK_DASHBOARD_1: any = {
  name: 'dashboards/dash-baseline-eval',
  title: 'XMC Baseline Tune Comparison',
  description: 'Comparative performance evaluation across training runs',
  owner: 'ml-researcher@google.com',
  charts: [MOCK_CHART_LOSS],
  chartLayout: {
    type: 'SIDE_BY_SIDE',
    positions: [
      { itemId: 'experiments/1/charts/chart-loss-perf', x: 0, y: 0, width: 12, height: 6 },
    ],
  },
};

export const MOCK_DASHBOARDS_LIST: any[] = [MOCK_DASHBOARD_1];

function buildMockSeriesPoints(minIter: number, maxIter: number): any[] {
  const points = [];
  const step = Math.max(1, Math.floor((maxIter - minIter) / 50));
  for (let iter = minIter; iter <= maxIter; iter += step) {
    const val = 2.5 * Math.exp(-0.003 * iter) + 0.1 * Math.sin(iter * 0.05);
    points.push({
      iteration: iter,
      timestamp: new Date(Date.now() - (maxIter - iter) * 1000).toISOString(),
      avgValue: Number(val.toFixed(4)),
      minValue: Number((val * 0.95).toFixed(4)),
      maxValue: Number((val * 1.05).toFixed(4)),
    });
  }
  return points;
}

/** Generates deterministic mock ChartData for local testing. */
export function generateMockChartData(request: any): any {
  const minIter = request.startIteration ?? 0;
  const maxIter = request.endIteration ?? 1000;
  const points = buildMockSeriesPoints(minIter, maxIter);
  return {
    name: request.name,
    xid: 1,
    plotsData: [
      {
        plotId: 'plot_summary_loss',
        type: 'SUMMARY_BOX',
        summaryData: {
          metricName: 'loss/train',
          latestValue: points[points.length - 1]?.avgValue ?? 0.12,
          minValue: 0.11,
          maxValue: 2.5,
          meanValue: 0.45,
          latestIteration: maxIter,
          latestTimestamp: new Date().toISOString(),
        },
      },
      {
        plotId: 'plot_curve_loss',
        type: 'SCATTER',
        scatterSeries: {
          metricName: 'loss/train',
          workUnitId: 'work-unit-1',
          xid: 1,
          points,
        },
      },
    ],
  };
}
