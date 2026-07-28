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

import { Injectable } from '@angular/core';
import { Observable, of } from 'rxjs';

import {
  HealthStatusResponse,
  ListConfigEntriesResponse,
  UserInfoResponse,
} from 'xmanager_cloud/health_service/proto/api.jsonpb_decls';

import { HealthService } from '../health_service';

/**
 * Mock implementation of HealthService for testing.
 */
@Injectable({ providedIn: 'root' })
export class MockHealthService extends HealthService {
  override getHealthStatus(): Observable<HealthStatusResponse> {
    return of({
      healthState: 'HEALTH_STATE_HEALTHY',
      systemInfo: {
        appVersion: 'mock-app-version-1.0.0',
        essDbSchemaVersion: '6',
        xidDbSchemaVersion: '1',
        observabilityUrl: 'http://mock-observability-url',
        isSchedulingPaused: false,
      },
      components: [
        {
          name: 'ESS',
          type: 'COMPONENT_TYPE_CORE_SERVICE',
          healthState: 'HEALTH_STATE_HEALTHY',
        },
        {
          name: 'XID',
          type: 'COMPONENT_TYPE_CORE_SERVICE',
          healthState: 'HEALTH_STATE_HEALTHY',
        },
      ],
      healthCheckTime: { seconds: 1717804800, nanos: 0 },
    } as any);
  }

  override getHealthConfigs(): Observable<ListConfigEntriesResponse> {
    return of({
      configGroups: [
        {
          displayName: 'Health Service Config',
          configs: [
            { key: 'Port', value: '8080' },
            { key: 'GRPCPort', value: '50051' },
            { key: 'ProjectID', value: 'mock-project' },
          ],
        },
      ],
    } as any);
  }

  override getUserInfo(): Observable<UserInfoResponse> {
    return of({
      userEmail: 'mock-user@google.com',
      privileges: ['PRIVILEGE_ACCESS_ADMIN_PANEL'],
    } as any);
  }
}
