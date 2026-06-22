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
import 'jasmine';

import { provideHttpClient } from '@angular/common/http';
import { firstValueFrom } from 'rxjs';

import { EXPERIMENT_1, EXPERIMENT_LIST } from 'xmanager_cloud/ui/app/common/mock_data';
import { cleanState, get, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { MockExperimentService } from './mock_experiment_service';

describe('MockExperimentService', () => {
  const mockState = cleanState(() => {
    setupModule({
      providers: [provideHttpClient(), MockExperimentService],
    });

    return {
      service: get(MockExperimentService),
    };
  }, beforeEach);

  it('should return mock search experiments', async () => {
    const service = mockState.service as MockExperimentService;
    const response = await firstValueFrom(
      service.searchExperiments({ filter: 'me' }),
    );
    expect(response.experiments).toEqual(EXPERIMENT_LIST.slice(0, 4));
    expect(response.nextPageToken).toBe('page_2');
  });

  it('should return mock get experiment', async () => {
    const service = mockState.service as MockExperimentService;
    const response = await firstValueFrom(service.getExperiment('1'));
    expect(response).toEqual(EXPERIMENT_1);
  });
});
