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

import { ChangeDetectionStrategy, Component, computed, inject, input, signal } from '@angular/core';
import { toObservable, toSignal } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { BehaviorSubject, catchError, combineLatest, of, switchMap, tap } from 'rxjs';

import { ChartArtifactPayload, ChartTypedArtifact } from 'app/common/artifacts/interfaces';
import { PlotDataPipe } from 'app/common/components/dashboard/chart_card/chart_card';
import { PlotCardComponent } from 'app/common/components/dashboard/plot_card/plot_card';
import { ErrorBannerComponent } from 'app/common/components/error_banner/error_banner';
import { PlotData, PlotSpec, PlotType } from 'app/service/dashboard_models';
import { DashboardService } from 'app/service/dashboard_service';

/**
 * Reports whether a plot spec is a KPI summary box.
 *
 * The type arrives either as the proto JSON name (which is what `PlotType.SUMMARY_BOX` is)
 * or as the enum's integer tag, depending on which serializer produced the chart, so both
 * spellings are accepted. `ChartCardComponent` filters its plots the same way.
 */
export function isSummaryBoxPlot(plot: PlotSpec): boolean {
  return plot.type === PlotType.SUMMARY_BOX || (plot.type as unknown) === 1;
}

/** A ChartArtifact provisioning status, as its proto JSON name. */
type ChartStatus = NonNullable<ChartArtifactPayload['status']>;

/** ChartArtifact.Status JSON names, indexed by the enum tag they are declared with. */
const CHART_STATUS_BY_TAG: readonly ChartStatus[] = [
  'STATUS_UNSPECIFIED',
  'PROVISIONING',
  'SUCCESS',
  'FAILED',
];

/**
 * Normalises a ChartArtifact status to its proto JSON name.
 *
 * Like `PlotSpec.type` above, the status arrives either as the JSON name or as the enum's
 * integer tag depending on the serializer. Comparing only against the names would let a
 * numerically encoded FAILED fall through as though the chart were fine, hiding the very
 * error message that status carries.
 */
export function chartStatusName(status: unknown): ChartStatus | undefined {
  if (typeof status === 'number') {
    return CHART_STATUS_BY_TAG[status];
  }
  return typeof status === 'string' ? (status as ChartStatus) : undefined;
}

/**
 * Full-width rendering of one ChartArtifact's plots on the work unit Artifacts tab.
 *
 * The artifact card in the pipeline column can only list the artifact's metric names: it is
 * a third of the tab wide, which is not enough room for a plot. This component draws the
 * Chart the Experiment State Server provisioned for the artifact, taking the plot specs
 * from the artifact payload and the series data from the Dashboard Service.
 */
@Component({
  selector: 'xmc-chart-artifact-view',
  standalone: true,
  templateUrl: './chart_artifact_view.ng.html',
  styleUrl: './chart_artifact_view.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    ErrorBannerComponent,
    MatButtonModule,
    MatIconModule,
    MatProgressSpinnerModule,
    PlotCardComponent,
    PlotDataPipe,
  ],
})
export class ChartArtifactView {
  readonly artifact = input.required<ChartTypedArtifact>();

  private readonly dashboardService = inject(DashboardService);
  private readonly refreshTrigger$ = new BehaviorSubject<void>(undefined);

  protected readonly isLoading = signal<boolean>(false);
  protected readonly error = signal<string | null>(null);

  private readonly payload = computed(() => this.artifact().payload);

  protected readonly title = computed(() => this.payload().title || 'Chart artifact');
  protected readonly metricNames = computed(() => this.payload().metricNames ?? []);

  /** Resource name of the provisioned chart, or '' while the artifact has none. */
  private readonly chartName = computed(() => this.payload().chart?.name ?? '');

  private readonly plots = computed<PlotSpec[]>(() => this.payload().chart?.plots ?? []);

  protected readonly summaryPlots = computed(() => this.plots().filter(isSummaryBoxPlot));
  protected readonly curvePlots = computed(() =>
    this.plots().filter((plot) => !isSummaryBoxPlot(plot)),
  );

  /**
   * Why there is nothing to draw, or null when the chart is renderable.
   *
   * A ChartArtifact is provisioned asynchronously, so it spends its first moments naming
   * metrics it cannot yet plot. Saying which of those states it is in beats a blank panel.
   */
  protected readonly notice = computed<string | null>(() => {
    const payload = this.payload();
    const status = chartStatusName(payload.status);
    if (status === 'FAILED') {
      return payload.errorMessage || 'The server could not provision a chart for this artifact.';
    }
    if (!this.chartName()) {
      return status === 'SUCCESS'
        ? 'This artifact has no provisioned chart to draw.'
        : 'The chart for this artifact is still being provisioned. Reload in a moment.';
    }
    if (!this.plots().length) {
      return 'The provisioned chart declares no plots, so there is nothing to draw.';
    }
    return null;
  });

  /**
   * The chart to fetch series for, or '' when there is nothing worth asking about.
   *
   * A chart declaring no plots has nowhere to put a response, so fetching one spends a
   * GetChartData round trip -- and a data engine query per plot on the other end -- to
   * populate a map the template never reads.
   */
  private readonly fetchTarget = computed(() => (this.plots().length ? this.chartName() : ''));

  protected readonly chartData = toSignal(
    combineLatest([toObservable(this.fetchTarget), this.refreshTrigger$]).pipe(
      tap(() => {
        this.isLoading.set(true);
        this.error.set(null);
      }),
      switchMap(([name]) => {
        // An unprovisioned artifact has no chart to ask the Dashboard Service about; calling
        // GetChartData with an empty name would only turn the wait into an error message.
        if (!name) {
          this.isLoading.set(false);
          return of(undefined);
        }
        return this.dashboardService.getChartData({ name }).pipe(
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

  protected readonly plotDataMap = computed(() => {
    const map = new Map<string, PlotData>();
    for (const plotData of this.chartData()?.plotsData ?? []) {
      if (plotData.plotId) {
        map.set(plotData.plotId, plotData);
      }
    }
    return map;
  });

  fetchChartData(): void {
    this.refreshTrigger$.next();
  }
}
