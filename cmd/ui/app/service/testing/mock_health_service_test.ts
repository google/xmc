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

import { cleanState, get, setupModule } from 'xmanager_cloud/ui/app/testing/test';

import { MockHealthService } from './mock_health_service';

describe('MockHealthService', () => {
  const mockState = cleanState(() => {
    setupModule({
      providers: [provideHttpClient(), MockHealthService],
    });

    return {
      service: get(MockHealthService),
    };
  }, beforeEach);

  it('should return mock health status', async () => {
    const service = mockState.service as MockHealthService;
    const response = await firstValueFrom(service.getHealthStatus());
    expect(response.healthState).toBe('HEALTH_STATE_HEALTHY');
    expect(response.systemInfo.appVersion).toBe('mock-app-version-1.0.0');
  });

  it('should return mock health configs', async () => {
    const service = mockState.service as MockHealthService;
    const response = await firstValueFrom(service.getHealthConfigs());
    expect(response.configGroups?.length).toBeGreaterThan(0);
    expect(response.configGroups?.[0].displayName).toBe('Health Service Config');
  });

  it('should return mock user info', async () => {
    const service = mockState.service as MockHealthService;
    const response = await firstValueFrom(service.getUserInfo());
    expect(response.userEmail).toBe('mock-user@google.com');
    expect(response.privileges).toContain('PRIVILEGE_ACCESS_ADMIN_PANEL');
  });
});
