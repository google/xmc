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
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { catchError, of, switchMap, tap } from 'rxjs';

import { ChartTypedArtifact } from 'xmanager_cloud/ui/app/common/artifacts/interfaces';
import { PlotDataPipe } from 'xmanager_cloud/ui/app/common/components/dashboard/chart_card/chart_card';
import { PlotCardComponent } from 'xmanager_cloud/ui/app/common/components/dashboard/plot_card/plot_card';
import { PlotData, PlotSpec, PlotType } from 'xmanager_cloud/ui/app/service/dashboard_models';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';

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
  imports: [MatProgressSpinnerModule, PlotCardComponent, PlotDataPipe],
})
export class ChartArtifactView {
  readonly artifact = input.required<ChartTypedArtifact>();

  private readonly dashboardService = inject(DashboardService);

  protected readonly isLoading = signal<boolean>(false);

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

  protected readonly chartData = toSignal(
    toObservable(this.chartName).pipe(
      tap(() => {
        this.isLoading.set(true);
      }),
      switchMap((name) => {
        // An unprovisioned artifact has no chart to ask the Dashboard Service about; calling
        // GetChartData with an empty name would only turn the wait into an error message.
        if (!name) {
          this.isLoading.set(false);
          return of(undefined);
        }
        return this.dashboardService.getChartData({ name }).pipe(
          tap(() => this.isLoading.set(false)),
          catchError(() => {
            // The spinner has to come down even when the fetch does not arrive, or a failed
            // request leaves the panel spinning with nothing on the way to replace it.
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
}
