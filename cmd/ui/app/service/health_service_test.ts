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
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { firstValueFrom } from 'rxjs';

import {
  GetSchedulingStatusResponse,
  HealthStatusResponse,
  ListConfigEntriesResponse,
  UserInfoResponse,
} from 'app/protos/health_service';

import { cleanState, get, setupModule, setupObservableMatchers } from '../testing/test';
import { XMC_API_DOMAIN } from './constants';
import { FeatureFlagService } from './feature_flag_service';
import { HealthService, PRIVILEGE_ACCESS_ADMIN_PANEL } from './health_service';

describe('HealthService', () => {
  beforeAll(setupObservableMatchers);

  const state = cleanState(() => {
    let adminPanelEnabledValue = false;
    const featureFlagServiceSpy = {
      get isAdminPanelEnabled() {
        return adminPanelEnabledValue;
      },
    };

    setupModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        HealthService,
        { provide: FeatureFlagService, useValue: featureFlagServiceSpy },
      ],
    });

    return {
      service: get(HealthService),
      httpMock: get(HttpTestingController),
      setAdminPanelEnabled: (val: boolean) => {
        adminPanelEnabledValue = val;
      },
    };
  }, beforeEach);

  afterEach(() => {
    state.httpMock.verify();
  });

  describe('getHealthStatus', () => {
    const mockResponse: HealthStatusResponse = {
      healthState: 'HEALTH_STATE_HEALTHY',
      systemInfo: { appVersion: '1.0' },
    };

    it('should make api call correctly', async () => {
      const responsePromise = firstValueFrom(state.service.getHealthStatus());
      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/health/status`);

      req.flush(mockResponse);
      const response = await responsePromise;

      expect(req.request.method).toBe('GET');
      expect(response).toEqual(mockResponse);
    });
  });

  describe('getHealthConfigs', () => {
    const mockResponse: ListConfigEntriesResponse = {
      configGroups: [{ displayName: 'Group 1' }],
    };

    it('should make api call correctly', async () => {
      const responsePromise = firstValueFrom(state.service.getHealthConfigs());
      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/health/configs`);

      req.flush(mockResponse);
      const response = await responsePromise;

      expect(req.request.method).toBe('GET');
      expect(response).toEqual(mockResponse);
    });
  });

  describe('getUserInfo', () => {
    const mockResponse: UserInfoResponse = {
      userEmail: 'user@example.com',
      privileges: ['PRIVILEGE_ACCESS_ADMIN_PANEL'],
    };

    it('should make api call correctly', async () => {
      const responsePromise = firstValueFrom(state.service.getUserInfo());
      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/health/userinfo`);

      req.flush(mockResponse);
      const response = await responsePromise;

      expect(req.request.method).toBe('GET');
      expect(response).toEqual(mockResponse);
    });
  });

  describe('isAdminPanelAccessible', () => {
    it('should return false if feature flag is disabled', async () => {
      state.setAdminPanelEnabled(false);

      const result = await firstValueFrom(state.service.isAdminPanelAccessible());

      expect(result).toBeFalse();
    });

    it('should return true if feature flag is enabled and user has privilege', async () => {
      state.setAdminPanelEnabled(true);
      const mockUserInfo: UserInfoResponse = {
        privileges: [PRIVILEGE_ACCESS_ADMIN_PANEL],
      };

      const responsePromise = firstValueFrom(state.service.isAdminPanelAccessible());
      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/health/userinfo`);
      req.flush(mockUserInfo);
      const result = await responsePromise;

      expect(result).toBeTrue();
    });

    it('should return false if feature flag is enabled but user lacks privilege', async () => {
      state.setAdminPanelEnabled(true);
      const mockUserInfo: UserInfoResponse = {
        privileges: [],
      };

      const responsePromise = firstValueFrom(state.service.isAdminPanelAccessible());
      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/health/userinfo`);
      req.flush(mockUserInfo);
      const result = await responsePromise;

      expect(result).toBeFalse();
    });
  });

  describe('getSchedulingStatus', () => {
    const mockResponse: GetSchedulingStatusResponse = {
      isSchedulingPaused: true,
    };

    it('should GET /health/scheduling with credentials', async () => {
      const responsePromise = firstValueFrom(state.service.getSchedulingStatus());
      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/health/scheduling`);

      expect(req.request.method).toBe('GET');
      expect(req.request.withCredentials).toBeTrue();
      req.flush(mockResponse);
      const response = await responsePromise;

      expect(response).toEqual(mockResponse);
    });
  });

  describe('pauseScheduling', () => {
    it('should POST to /health/scheduling:pause with credentials', async () => {
      const responsePromise = firstValueFrom(state.service.pauseScheduling());
      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/health/scheduling:pause`);

      expect(req.request.method).toBe('POST');
      expect(req.request.withCredentials).toBeTrue();
      req.flush({});
      const response = await responsePromise;

      expect(response).toEqual({});
    });
  });

  describe('resumeScheduling', () => {
    it('should POST to /health/scheduling:resume with credentials', async () => {
      const responsePromise = firstValueFrom(state.service.resumeScheduling());
      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/health/scheduling:resume`);

      expect(req.request.method).toBe('POST');
      expect(req.request.withCredentials).toBeTrue();
      req.flush({});
      const response = await responsePromise;

      expect(response).toEqual({});
    });
  });
});
