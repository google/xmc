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

import { Chart, Dashboard, LayoutType, PlotSpec, PlotType } from 'app/service/dashboard_models';

/**
 * Creates mock plot specifications for testing metrics dashboards.
 */
export function createMockPlots(): PlotSpec[] {
  return [
    {
      plotId: 'val_loss_kpi',
      title: 'Last validation loss',
      type: PlotType.SUMMARY_BOX,
      data: { metrics: ['val/loss'] },
    },
    {
      plotId: 'train_acc_kpi',
      title: 'Last training accuracy',
      type: PlotType.SUMMARY_BOX,
      data: { metrics: ['train/accuracy'] },
    },
    {
      plotId: 'loss_vs_steps',
      title: 'Validation loss vs steps',
      type: PlotType.SCATTER,
      data: { metrics: ['val/loss'] },
    },
    {
      plotId: 'acc_vs_steps',
      title: 'Training accuracy vs steps',
      type: PlotType.SCATTER,
      data: { metrics: ['train/accuracy'] },
    },
  ];
}

/**
 * Creates mock comparison charts representing baseline and tuned experiments.
 */
export function createMockComparisonCharts(): Chart[] {
  const experiments = [
    { xid: 234911122, name: 'baseline', title: 'VCC Baseline' },
    { xid: 234911123, name: 'tuned_v1', title: 'VCC tuned_v1' },
    { xid: 234911124, name: 'tuned_v2', title: 'VCC tuned_v2' },
  ];

  return experiments.map((exp) => ({
    name: `experiments/${exp.xid}/charts/${exp.name}`,
    title: exp.title,
    xid: exp.xid,
    wid: 1,
    plotLayout: { type: LayoutType.GRID },
    plots: createMockPlots(),
  }));
}

/**
 * Creates a mock VCC baseline tune comparison dashboard.
 */
export function createMockComparisonDashboard(overrides?: Partial<Dashboard>): Dashboard {
  return {
    name: 'dashboards/vcc_comparison',
    title: 'VCC baseline tune comparison',
    description: 'Comparative evaluation of VCC baseline tuning across hyperparameter runs',
    chartIds: ['baseline', 'tuned_v1', 'tuned_v2'],
    ...overrides,
  };
}

/**
 * Creates a standard list of mock dashboards for list and overview testing.
 */
export function createMockDashboardList(): Dashboard[] {
  return [
    createMockComparisonDashboard(),
    {
      name: 'dashboards/testdata_overview',
      title: 'Data Engine Golden Testcases',
      description: 'Overview of all testcases',
      chartIds: [],
    },
  ];
}
