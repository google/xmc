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
import { provideHttpClient } from '@angular/common/http';
import { firstValueFrom } from 'rxjs';

import { ARTIFACT_LIST } from 'app/common/mock_data';
import { cleanState, get, setupModule } from 'app/testing/test';

import { MockArtifactService } from './mock_artifact_service';

describe('MockArtifactService', () => {
  const mockState = cleanState(() => {
    setupModule({
      providers: [provideHttpClient(), MockArtifactService],
    });

    return {
      service: get(MockArtifactService),
    };
  }, beforeEach);

  it('should return mock artifacts', async () => {
    const service = mockState.service as MockArtifactService;
    const response = await firstValueFrom(service.listArtifacts({ parent: 'experiments/1' }));
    expect(response.artifacts).toEqual(ARTIFACT_LIST);
    expect(response.nextPageToken).toBe('page_token');
  });
});
