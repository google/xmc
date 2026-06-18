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
import 'jasmine';

import { provideHttpClient } from '@angular/common/http';
import { firstValueFrom } from 'rxjs';

import { MOCK_HISTORICAL_STATUS_MESSAGES } from 'xmanager_cloud/ui/app/common/mock_data';
import { cleanState, get, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { MockStatusMessageService } from './mock_status_message_service';

describe('MockStatusMessageService', () => {
  const state = cleanState(() => {
    setupModule({
      providers: [provideHttpClient(), MockStatusMessageService],
    });

    return {
      service: get(MockStatusMessageService),
    };
  }, beforeEach);

  it('should return mock status messages', async () => {
    const service = state.service as MockStatusMessageService;
    const response = await firstValueFrom(
      service.listStatusMessages({ parent: 'experiments/1' }),
    );
    expect(response.statusMessages).toEqual(MOCK_HISTORICAL_STATUS_MESSAGES);
    expect(response.nextPageToken).toBe('page_token');
  });
});
