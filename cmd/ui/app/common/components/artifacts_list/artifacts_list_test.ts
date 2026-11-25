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

import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { firstValueFrom, of } from 'rxjs';

import { ArtifactSingle } from 'app/common/artifacts/interfaces';
import { Artifact, ListArtifactsResponse } from 'app/protos/experiment_state_server';
import { ArtifactService } from 'app/service/artifact_service';
import { bootstrap, setupModule } from 'app/testing/test';

import { ArtifactFilter, ArtifactsList } from './artifacts_list';

describe('ArtifactsList Component', () => {
  let artifactServiceSpy: jasmine.SpyObj<ArtifactService>;

  const mockArtifacts: Artifact[] = [
    {
      name: 'artifacts/1',
      parent: 'experiments/1',
      lifecyclePhase: 'INPUT',
      payload: {
        text: { title: 'Exp Input', text: 'data' },
      },
    },
    {
      name: 'artifacts/2',
      parent: 'experiments/1/workUnits/1',
      lifecyclePhase: 'INPUT',
      payload: {
        text: { title: 'WU Input', text: 'data' },
      },
    },
    {
      name: 'artifacts/3',
      parent: 'experiments/1',
      lifecyclePhase: 'OUTPUT',
      payload: {
        text: { title: 'Exp Output', text: 'data' },
      },
    },
    {
      name: 'artifacts/4',
      parent: 'experiments/1/workUnits/2',
      lifecyclePhase: 'OUTPUT',
      payload: {
        text: { title: 'WU Output', text: 'data' },
      },
    },
  ];

  beforeEach(() => {
    artifactServiceSpy = jasmine.createSpyObj<ArtifactService>('ArtifactService', [
      'listArtifacts',
    ]);
    artifactServiceSpy.listArtifacts.and.returnValue(
      of<ListArtifactsResponse>({
        artifacts: mockArtifacts,
      }),
    );

    setupModule({
      imports: [ArtifactsList],
      providers: [
        provideNoopAnimations(),
        provideRouter([]),
        { provide: ArtifactService, useValue: artifactServiceSpy },
      ],
    });
  });

  it('should enable both EXPERIMENT and WORK_UNIT filters by default on experiment page', async () => {
    const comp: ArtifactsList = bootstrap(ArtifactsList, {
      parentName: 'experiments/1',
    });
    expect(comp).toBeTruthy();

    const filter = comp['artifactFilter'].value;
    expect(filter).toContain(ArtifactFilter.EXPERIMENT);
    expect(filter).toContain(ArtifactFilter.WORK_UNIT);
    expect(comp['showFilterToggle']).toBeTrue();
    expect(comp['isWorkUnitPage']).toBeFalse();

    const inputItems = await firstValueFrom(comp['inputArtifacts$']);
    // Both experiment-level and work-unit level input artifacts should be present
    expect(inputItems.length).toBe(2);

    const outputItems = await firstValueFrom(comp['outputArtifacts$']);
    // Both experiment-level and work-unit level output artifacts should be present
    expect(outputItems.length).toBe(2);
  });

  it('should filter to only experiment-level artifacts when WORK_UNIT filter is unchecked', async () => {
    const comp: ArtifactsList = bootstrap(ArtifactsList, {
      parentName: 'experiments/1',
    });
    comp['onFilterChange']([ArtifactFilter.EXPERIMENT]);

    const inputItems = await firstValueFrom(comp['inputArtifacts$']);
    expect(inputItems.length).toBe(1);
    expect((inputItems[0] as ArtifactSingle).artifact.artifact.name).toBe('artifacts/1');

    const outputItems = await firstValueFrom(comp['outputArtifacts$']);
    expect(outputItems.length).toBe(1);
    expect((outputItems[0] as ArtifactSingle).artifact.artifact.name).toBe('artifacts/3');
  });

  it('should filter to only work-unit level artifacts when EXPERIMENT filter is unchecked', async () => {
    const comp: ArtifactsList = bootstrap(ArtifactsList, {
      parentName: 'experiments/1',
    });
    comp['onFilterChange']([ArtifactFilter.WORK_UNIT]);

    const inputItems = await firstValueFrom(comp['inputArtifacts$']);
    expect(inputItems.length).toBe(1);
    expect((inputItems[0] as ArtifactSingle).artifact.artifact.name).toBe('artifacts/2');

    const outputItems = await firstValueFrom(comp['outputArtifacts$']);
    expect(outputItems.length).toBe(1);
    expect((outputItems[0] as ArtifactSingle).artifact.artifact.name).toBe('artifacts/4');
  });

  it('should return empty lists when all filters are deselected', async () => {
    const comp: ArtifactsList = bootstrap(ArtifactsList, {
      parentName: 'experiments/1',
    });
    comp['onFilterChange']([]);

    const inputItems = await firstValueFrom(comp['inputArtifacts$']);
    expect(inputItems.length).toBe(0);

    const outputItems = await firstValueFrom(comp['outputArtifacts$']);
    expect(outputItems.length).toBe(0);
  });

  it('should disable filter toggle and return all artifacts on work unit page', async () => {
    const comp: ArtifactsList = bootstrap(ArtifactsList, {
      parentName: 'experiments/1/workUnits/1',
    });
    expect(comp['showFilterToggle']).toBeFalse();
    expect(comp['isWorkUnitPage']).toBeTrue();

    const inputItems = await firstValueFrom(comp['inputArtifacts$']);
    expect(inputItems.length).toBe(2);
  });
});
