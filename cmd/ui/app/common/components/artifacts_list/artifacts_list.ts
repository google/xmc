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
import {AsyncPipe} from '@angular/common';
import {ChangeDetectionStrategy, Component, inject, input, OnInit} from '@angular/core';
import {FormsModule} from '@angular/forms';
import {MatButtonToggleModule} from '@angular/material/button-toggle';
import {MatIconModule} from '@angular/material/icon';
import {ListArtifactsResponse} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {Artifact} from 'xmanager_cloud/experiment_state_server/proto/artifact.jsonpb_decls';
import {BehaviorSubject, combineLatest, Observable} from 'rxjs';
import {map, shareReplay} from 'rxjs/operators';

import {ArtifactService} from '../../../service/artifact_service';
import {groupAndSortArtifacts} from '../../artifacts/artifact_utils';
import {ArtifactListItem} from '../../artifacts/interfaces';
import {parseWorkUnitName} from '../../resource_name_extractor';

import {ArtifactsColumn} from './artifacts_column/artifacts_column';

/** Artifact filter options. */
enum ArtifactFilter {
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
  styleUrl: './artifacts_list.css',
  imports: [
    AsyncPipe,
    MatIconModule,
    ArtifactsColumn,
    MatButtonToggleModule,
    FormsModule,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class ArtifactsList implements OnInit {
  readonly parentName = input.required<string>();
  readonly groupingThreshold = input<number>(3);

  private readonly artifactService = inject(ArtifactService);

  protected artifactsResponse$!: Observable<ListArtifactsResponse>;
  protected artifacts$!: Observable<readonly Artifact[]>;
  protected inputArtifacts$!: Observable<readonly ArtifactListItem[]>;
  protected executionArtifacts$!: Observable<readonly ArtifactListItem[]>;
  protected outputArtifacts$!: Observable<readonly ArtifactListItem[]>;
  protected nextPageToken$!: Observable<string|undefined>;
  protected showTruncationMessage$!: Observable<boolean>;

  protected artifactFilter =
      new BehaviorSubject<ArtifactFilter[]>([ArtifactFilter.EXPERIMENT]);
  protected readonly ArtifactFilter = ArtifactFilter;
  protected showFilterToggle = false;

  ngOnInit() {
    this.showFilterToggle = parseWorkUnitName(this.parentName()) === null;
    this.artifactsResponse$ =
        this.artifactService
            .listArtifacts({
              parent: this.parentName(),
            })
            .pipe(shareReplay({bufferSize: 1, refCount: true}));
    this.artifacts$ = this.artifactsResponse$.pipe(
        map((response) => response.artifacts ?? []),
    );
    this.nextPageToken$ = this.artifactsResponse$.pipe(
        map((response) => response.nextPageToken),
    );

    const filteredArtifacts$: Observable<readonly Artifact[]> =
        combineLatest([
          this.artifacts$,
          this.artifactFilter,
        ])
            .pipe(
                map(([artifacts, filter]) => {
                  if (!this.showFilterToggle ||
                      (filter.includes(ArtifactFilter.EXPERIMENT) &&
                       filter.includes(ArtifactFilter.WORK_UNIT))) {
                    return artifacts;
                  }
                  if (filter.length === 0) {
                    return [];
                  }
                  return artifacts.filter(artifact => {
                    const isWorkUnitArtifact = artifact.parent ?
                        parseWorkUnitName(artifact.parent) !== null :
                        false;
                    if (filter.includes(ArtifactFilter.EXPERIMENT) &&
                        !isWorkUnitArtifact) {
                      return true;
                    }
                    if (filter.includes(ArtifactFilter.WORK_UNIT) &&
                        isWorkUnitArtifact) {
                      return true;
                    }
                    return false;
                  });
                }),
            );

    this.showTruncationMessage$ =
        combineLatest([
          this.nextPageToken$,
          filteredArtifacts$,
        ])
            .pipe(
                map(([token, artifacts]) => !!token && artifacts.length > 0),
            );

    this.inputArtifacts$ = filteredArtifacts$.pipe(
        map(artifacts => artifacts.filter(a => a.lifecyclePhase === 'INPUT')),
        map(artifacts => groupAndSortArtifacts(
                artifacts, 'INPUT', this.groupingThreshold())));
    this.executionArtifacts$ = filteredArtifacts$.pipe(
        map(artifacts =>
                artifacts.filter(a => a.lifecyclePhase === 'EXECUTION')),
        map(artifacts => groupAndSortArtifacts(
                artifacts, 'EXECUTION', this.groupingThreshold())));
    this.outputArtifacts$ = filteredArtifacts$.pipe(
        map(artifacts => artifacts.filter(
                a => a.lifecyclePhase === 'OUTPUT' || !a.lifecyclePhase)),
        map(artifacts => groupAndSortArtifacts(
                artifacts, 'OUTPUT', this.groupingThreshold())));
  }

  protected onFilterChange(value: ArtifactFilter[]) {
    this.artifactFilter.next(value);
  }
}
