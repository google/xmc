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
import {
  ChangeDetectionStrategy,
  Component,
  computed,
  inject,
  input,
  output,
  Pipe,
  PipeTransform,
  signal,
} from '@angular/core';
import { toObservable, toSignal } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { RouterModule } from '@angular/router';
import { BehaviorSubject, catchError, combineLatest, of, switchMap, tap } from 'rxjs';

import { Chart, PlotData, PlotSpec, PlotType } from 'app/service/dashboard_models';
import { DashboardService } from 'app/service/dashboard_service';

import { PlotCardComponent } from '../plot_card/plot_card';

/**
 * Resolves a unique tracking key and removal identifier for a chart.
 */
export function getChartKey(chart: Chart): string {
  return chart.name || `${chart.xid}_${chart.wid ?? ''}_${chart.title ?? ''}`;
}

/**
 * Checks whether a chart is associated with a specific work unit (wid > 0).
 * In proto3, wid defaults to 0 when unset (indicating an experiment-level chart).
 */
export function hasWorkUnit(chart: Chart): boolean {
  return chart.wid !== undefined && chart.wid !== null && Number(chart.wid) > 0;
}

/**
 * Pure Angular pipe to look up a PlotData object by plot ID from a plot data map.
 */
@Pipe({
  name: 'plotData',
  standalone: true,
  pure: true,
})
export class PlotDataPipe implements PipeTransform {
  transform(
    plotId: string | null | undefined,
    plotDataMap?: ReadonlyMap<string, PlotData> | null,
  ): PlotData | undefined {
    if (!plotId || !plotDataMap) {
      return undefined;
    }
    return plotDataMap.get(plotId);
  }
}

/**
 * Chart card component representing an experiment's visualization column.
 */
@Component({
  selector: 'xmc-chart-card',
  standalone: true,
  templateUrl: './chart_card.ng.html',
  styleUrl: './chart_card.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    RouterModule,
    MatCardModule,
    MatButtonModule,
    MatIconModule,
    MatMenuModule,
    MatProgressSpinnerModule,
    PlotCardComponent,
    PlotDataPipe,
  ],
})
export class ChartCardComponent {
  readonly chart = input.required<Chart>();
  readonly isBaseline = input<boolean>(false);
  readonly remove = output<string>();

  private readonly dashboardService = inject(DashboardService);
  private readonly refreshTrigger$ = new BehaviorSubject<void>(undefined);

  protected readonly isLoading = signal<boolean>(false);
  protected readonly error = signal<string | null>(null);

  protected readonly chartData = toSignal(
    combineLatest([toObservable(this.chart), this.refreshTrigger$]).pipe(
      tap(() => {
        this.isLoading.set(true);
        this.error.set(null);
      }),
      switchMap(([c]) => {
        const chartName =
          c.name || `experiments/${c.xid}/workUnits/${c.wid ?? 1}/charts/${c.title || 'default'}`;
        return this.dashboardService.getChartData({ name: chartName }).pipe(
          tap(() => this.isLoading.set(false)),
          catchError((err: { message?: string }) => {
            this.error.set(err?.message || 'Failed to load chart metrics');
            this.isLoading.set(false);
            return of(undefined);
          }),
        );
      }),
    ),
  );

  protected readonly headerTitle = computed(() => {
    const c = this.chart();
    const xidStr = `XID/${c.xid}`;
    const widStr = hasWorkUnit(c) ? ` - WorkUnitID ${c.wid}` : '';
    const titleStr = c.title ? ` - ${c.title}` : '';
    return `${xidStr}${titleStr}${widStr}`;
  });

  protected readonly headerUrl = computed(() => {
    const c = this.chart();
    if (hasWorkUnit(c)) {
      return `/experiments/${c.xid}/workUnits/${c.wid}`;
    }
    return `/experiments/${c.xid}`;
  });

  protected readonly summaryPlots = computed<PlotSpec[]>(() => {
    const plots = this.chart().plots ?? [];
    return plots.filter(
      (p) =>
        p.type === PlotType.SUMMARY_BOX ||
        (p.type as unknown) === 1 ||
        (p.type as unknown) === 'SUMMARY_BOX',
    );
  });

  protected readonly curvePlots = computed<PlotSpec[]>(() => {
    const plots = this.chart().plots ?? [];
    return plots.filter(
      (p) =>
        p.type !== PlotType.SUMMARY_BOX &&
        (p.type as unknown) !== 1 &&
        (p.type as unknown) !== 'SUMMARY_BOX',
    );
  });

  fetchChartData(): void {
    this.refreshTrigger$.next();
  }

  protected readonly plotDataMap = computed(() => {
    const map = new Map<string, PlotData>();
    for (const p of this.chartData()?.plotsData ?? []) {
      if (p.plotId) {
        map.set(p.plotId, p);
      }
    }
    return map;
  });

  onRemove(): void {
    this.remove.emit(getChartKey(this.chart()));
  }
}
