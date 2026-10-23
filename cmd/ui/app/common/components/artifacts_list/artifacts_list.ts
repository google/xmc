// Copyright 2025 Google LLC
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
import { AsyncPipe } from '@angular/common';
import { ChangeDetectionStrategy, Component, inject, input, OnInit } from '@angular/core';
import { toObservable } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatIconModule } from '@angular/material/icon';
import { BehaviorSubject, combineLatest, Observable, of } from 'rxjs';
import { map, shareReplay } from 'rxjs/operators';

import {
  groupAndSortArtifacts,
  sortArtifactsByPriority,
  toTypedArtifact,
} from 'app/common/artifacts/artifact_mapper';
import { ArtifactListItem, ChartTypedArtifact, PayloadType } from 'app/common/artifacts/interfaces';
import { parseWorkUnitName } from 'app/common/resource_name_extractor';
import { Artifact, ListArtifactsResponse } from 'app/protos/experiment_state_server';
import { ArtifactService } from 'app/service/artifact_service';

import { ArtifactsColumn } from './artifacts_column/artifacts_column';
import { ChartArtifactView } from './chart_artifact_view/chart_artifact_view';

/** Artifact filter options. */
export enum ArtifactFilter {
  EXPERIMENT = 'Experiment',
  WORK_UNIT = 'Work Unit',
}

/**
 * Display a list of artifacts for a single experiment.
 */
@Component({
  selector: 'xmc-artifacts-list',
  standalone: true,
  templateUrl: './artifacts_list.ng.html',
  styleUrl: './artifacts_list.scss',
  imports: [
    AsyncPipe,
    MatIconModule,
    ArtifactsColumn,
    ChartArtifactView,
    MatButtonToggleModule,
    FormsModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ArtifactsList implements OnInit {
  readonly parentName = input.required<string>();
  readonly groupingThreshold = input<number>(3);
  private readonly groupingThreshold$ = toObservable(this.groupingThreshold);

  private readonly artifactService = inject(ArtifactService);

  protected artifactsResponse$!: Observable<ListArtifactsResponse>;
  protected artifacts$!: Observable<readonly Artifact[]>;
  protected inputArtifacts$!: Observable<readonly ArtifactListItem[]>;
  protected executionArtifacts$!: Observable<readonly ArtifactListItem[]>;
  protected outputArtifacts$!: Observable<readonly ArtifactListItem[]>;
  protected chartArtifacts$!: Observable<readonly ChartTypedArtifact[]>;
  protected nextPageToken$!: Observable<string | undefined>;
  protected showTruncationMessage$!: Observable<boolean>;

  protected artifactFilter = new BehaviorSubject<ArtifactFilter[]>([
    ArtifactFilter.EXPERIMENT,
    ArtifactFilter.WORK_UNIT,
  ]);
  protected readonly ArtifactFilter = ArtifactFilter;
  protected showFilterToggle = false;
  protected isWorkUnitPage = false;

  ngOnInit() {
    this.isWorkUnitPage = parseWorkUnitName(this.parentName()) !== null;
    this.showFilterToggle = !this.isWorkUnitPage;
    this.initializeArtifactStreams();
  }

  private createPhaseStream(
    artifacts$: Observable<readonly Artifact[]>,
    phaseLabel: 'INPUT' | 'EXECUTION' | 'OUTPUT',
    phaseCheck: (p?: string) => boolean,
  ): Observable<readonly ArtifactListItem[]> {
    return combineLatest([artifacts$, this.groupingThreshold$]).pipe(
      map(([artifacts, threshold]) => {
        const typed = artifacts.filter((a) => phaseCheck(a.lifecyclePhase)).map(toTypedArtifact);
        return groupAndSortArtifacts(typed, phaseLabel, threshold);
      }),
    );
  }

  /**
   * Selects the chart artifacts that get a full-width band of their own.
   *
   * A chart artifact is also rendered as a card in whichever pipeline column its lifecycle
   * phase puts it, but a third of the tab is too narrow to plot anything in. The band goes
   * above the pipeline and the card stays where it is, as the index entry.
   *
   * Only on a work unit page: each band costs one GetChartData, and an experiment lists the
   * artifacts of every one of its work units, so drawing them all there would fan out to a
   * request per work unit on a page that never asked for plots.
   */
  private createChartArtifactStream(
    artifacts$: Observable<readonly Artifact[]>,
  ): Observable<readonly ChartTypedArtifact[]> {
    if (!this.isWorkUnitPage) {
      return of([]);
    }
    return artifacts$.pipe(
      map((artifacts) =>
        sortArtifactsByPriority(
          artifacts
            .map(toTypedArtifact)
            .filter((typed): typed is ChartTypedArtifact => typed.type === PayloadType.CHART),
        ),
      ),
    );
  }

  private initializeArtifactStreams() {
    this.artifactsResponse$ = this.artifactService
      .listArtifacts({
        parent: this.parentName(),
      })
      .pipe(shareReplay({ bufferSize: 1, refCount: true }));
    this.artifacts$ = this.artifactsResponse$.pipe(map((response) => response.artifacts ?? []));
    this.nextPageToken$ = this.artifactsResponse$.pipe(map((response) => response.nextPageToken));

    const filteredArtifacts$: Observable<readonly Artifact[]> = combineLatest([
      this.artifacts$,
      this.artifactFilter,
    ]).pipe(map(([artifacts, filter]) => this.filterArtifacts(artifacts, filter)));

    this.showTruncationMessage$ = combineLatest([this.nextPageToken$, filteredArtifacts$]).pipe(
      map(([token, artifacts]) => !!token && artifacts.length > 0),
    );
    this.chartArtifacts$ = this.createChartArtifactStream(filteredArtifacts$);

    this.inputArtifacts$ = this.createPhaseStream(
      filteredArtifacts$,
      'INPUT',
      (p) => p === 'INPUT',
    );
    this.executionArtifacts$ = this.createPhaseStream(
      filteredArtifacts$,
      'EXECUTION',
      (p) => p === 'EXECUTION',
    );
    this.outputArtifacts$ = this.createPhaseStream(
      filteredArtifacts$,
      'OUTPUT',
      (p) => p === 'OUTPUT' || !p,
    );
  }

  private filterArtifacts(
    artifacts: readonly Artifact[],
    filter: ArtifactFilter[],
  ): readonly Artifact[] {
    if (
      !this.showFilterToggle ||
      (filter.includes(ArtifactFilter.EXPERIMENT) && filter.includes(ArtifactFilter.WORK_UNIT))
    ) {
      return artifacts;
    }
    if (filter.length === 0) {
      return [];
    }
    return artifacts.filter((artifact) => {
      const isWorkUnitArtifact = !!artifact.parent && parseWorkUnitName(artifact.parent) !== null;
      const matchesExperiment = filter.includes(ArtifactFilter.EXPERIMENT) && !isWorkUnitArtifact;
      const matchesWorkUnit = filter.includes(ArtifactFilter.WORK_UNIT) && isWorkUnitArtifact;
      return matchesExperiment || matchesWorkUnit;
    });
  }

  protected onFilterChange(value: ArtifactFilter[]) {
    this.artifactFilter.next(value);
  }
}
