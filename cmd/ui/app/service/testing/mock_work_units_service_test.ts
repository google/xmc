// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.angular.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
import 'jasmine';

import { provideHttpClient } from '@angular/common/http';
import { firstValueFrom } from 'rxjs';

import { WORK_UNIT_1, WORK_UNIT_LIST } from 'xmanager_cloud/ui/app/common/mock_data';
import { cleanState, get, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { MockWorkUnitsService } from './mock_work_units_service';

describe('MockWorkUnitsService', () => {
  const state = cleanState(() => {
    setupModule({
      providers: [provideHttpClient(), MockWorkUnitsService],
    });

    return {
      service: get(MockWorkUnitsService),
    };
  }, beforeEach);

  it('should return mock work units', async () => {
    const service = state.service as MockWorkUnitsService;
    const response = await firstValueFrom(
      service.searchWorkUnits({ parent: 'experiments/1' }),
    );
    expect(response.workUnits).toEqual(WORK_UNIT_LIST);
    expect(response.nextPageToken).toBe('page_token');
  });

  it('should return mock single work unit', async () => {
    const service = state.service as MockWorkUnitsService;
    const response = await firstValueFrom(
      service.getWorkUnit({ name: 'experiments/1/workUnits/1' }),
    );
    expect(response).toEqual(WORK_UNIT_1);
  });
});
