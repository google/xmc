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

import { CommonModule } from '@angular/common';
import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { FormControl, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatChipsModule } from '@angular/material/chips';
import { MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatDividerModule } from '@angular/material/divider';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';

import {
  Chart,
  Dashboard,
  LayoutType,
  PlotSpec,
  PlotType,
} from 'xmanager_cloud/ui/app/service/dashboard_models';

export interface ComparisonExperimentEntry {
  xid: number;
  wid?: number;
  title?: string;
}

export function getDefaultPlotSpecs(): PlotSpec[] {
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

export function buildBaseChart(
  baseXid: number,
  baseWid: number,
  title: string | undefined,
  plots: PlotSpec[],
): Chart {
  return {
    name: `experiments/${baseXid}/charts/baseline`,
    title: title?.trim() || `Baseline Run (XID ${baseXid})`,
    xid: baseXid,
    wid: baseWid,
    plots,
    plotLayout: { type: LayoutType.GRID },
  };
}

export function buildComparisonCharts(
  comparisons: ComparisonExperimentEntry[],
  plots: PlotSpec[],
): Chart[] {
  return comparisons.map((comp, idx) => ({
    name: `experiments/${comp.xid}/charts/comparison_${idx + 1}`,
    title: comp.title || `Experiment ${comp.xid}`,
    xid: comp.xid,
    wid: comp.wid ?? 1,
    plots,
    plotLayout: { type: LayoutType.GRID },
  }));
}

export function buildDashboardResourceName(title: string): string {
  const slug = title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '');
  return `dashboards/${slug || `dash_${Date.now()}`}`;
}

/**
 * Dialog allowing researchers to create a new Performance Dashboard.
 * Configures base experiment ID, work unit ID, and customizable charts/plots.
 */
@Component({
  selector: 'xmc-create-dashboard-dialog',
  standalone: true,
  templateUrl: './create_dashboard_dialog.ng.html',
  styleUrl: './create_dashboard_dialog.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    ReactiveFormsModule,
    MatDialogModule,
    MatFormFieldModule,
    MatInputModule,
    MatSelectModule,
    MatButtonModule,
    MatIconModule,
    MatChipsModule,
    MatDividerModule,
    MatTooltipModule,
  ],
})
export class CreateDashboardDialogComponent {
  private readonly dialogRef = inject(MatDialogRef<CreateDashboardDialogComponent>);

  readonly PlotType = PlotType;

  readonly form = new FormGroup({
    title: new FormControl<string>('', [Validators.required, Validators.minLength(2)]),
    description: new FormControl<string>(''),
    baseXid: new FormControl<number | null>(null, [Validators.required, Validators.min(1)]),
    baseWid: new FormControl<number | null>(1, [Validators.min(0)]),
    baseChartTitle: new FormControl<string>(''),
  });

  // New plot builder form
  readonly newPlotForm = new FormGroup({
    type: new FormControl<PlotType>(PlotType.SUMMARY_BOX, [Validators.required]),
    metricName: new FormControl<string>('', [Validators.required, Validators.minLength(1)]),
    title: new FormControl<string>(''),
  });

  // Comparison experiment sub-form
  readonly newComparisonForm = new FormGroup({
    xid: new FormControl<number | null>(null, [Validators.min(1)]),
    wid: new FormControl<number | null>(1, [Validators.min(0)]),
    title: new FormControl<string>(''),
  });

  readonly plots = signal<PlotSpec[]>(getDefaultPlotSpecs());
  readonly comparisons = signal<ComparisonExperimentEntry[]>([]);

  addPlot(): void {
    if (this.newPlotForm.invalid) return;

    const metric = this.newPlotForm.value.metricName?.trim() ?? '';
    const type = this.newPlotForm.value.type ?? PlotType.SUMMARY_BOX;
    const defaultTitle = type === PlotType.SUMMARY_BOX ? `Last ${metric}` : `${metric} vs steps`;
    const title = this.newPlotForm.value.title?.trim() || defaultTitle;
    const plotId = `${metric.replace(/[^a-zA-Z0-9]/g, '_')}_${Date.now()}`;

    const newPlot: PlotSpec = {
      plotId,
      title,
      type,
      data: { metrics: [metric] },
    };

    this.plots.update((list) => [...list, newPlot]);
    this.newPlotForm.reset({
      type: PlotType.SUMMARY_BOX,
      metricName: '',
      title: '',
    });
  }

  removePlot(index: number): void {
    this.plots.update((list) => list.filter((_, i) => i !== index));
  }

  resetPlotsToDefault(): void {
    this.plots.set(getDefaultPlotSpecs());
  }

  addComparison(): void {
    const xid = this.newComparisonForm.value.xid;
    if (!xid || xid < 1) return;

    const entry: ComparisonExperimentEntry = {
      xid: Number(xid),
      wid: this.newComparisonForm.value.wid !== null ? Number(this.newComparisonForm.value.wid) : 1,
      title: this.newComparisonForm.value.title?.trim() || `Tuned Run (${xid})`,
    };

    this.comparisons.update((list) => [...list, entry]);
    this.newComparisonForm.reset({
      xid: null,
      wid: 1,
      title: '',
    });
  }

  removeComparison(index: number): void {
    this.comparisons.update((list) => list.filter((_, i) => i !== index));
  }

  onCancel(): void {
    this.dialogRef.close();
  }

  onSubmit(): void {
    if (this.form.invalid || this.plots().length === 0) return;

    const formVal = this.form.value;
    const baseXid = Number(formVal.baseXid);
    const baseWid =
      formVal.baseWid !== null && formVal.baseWid !== undefined ? Number(formVal.baseWid) : 1;
    const rawTitle = formVal.title?.trim() || `Dashboard for Experiment ${baseXid}`;

    const baseChart = buildBaseChart(
      baseXid,
      baseWid,
      formVal.baseChartTitle ?? undefined,
      this.plots(),
    );
    const comparisonCharts = buildComparisonCharts(this.comparisons(), this.plots());

    const dashboard: Dashboard = {
      name: buildDashboardResourceName(rawTitle),
      title: rawTitle,
      description: formVal.description?.trim() || undefined,
      chartLayout: { type: LayoutType.SIDE_BY_SIDE },
      charts: [baseChart, ...comparisonCharts],
    };

    this.dialogRef.close(dashboard);
  }
}
