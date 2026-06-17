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
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';

import { WORK_UNIT_1, WORK_UNIT_LIST } from 'xmanager_cloud/ui/app/common/mock_data';
import {
  cleanState,
  get,
  setupModule,
  setupObservableMatchers,
} from 'xmanager_cloud/ui/app/testing/test';

import { EXPERIMENT_STATE_SERVER_DOMAIN, USE_MOCK_DATA } from './constants';
import { MockWorkUnitsService } from './testing/mock_work_units_service';
import { WorkUnitsService } from './work_units_service';

describe('WorkUnitsService', () => {
  beforeAll(setupObservableMatchers);

  describe('with real data', () => {
    const state = cleanState(() => {
      setupModule({
        providers: [
          provideHttpClient(),
          provideHttpClientTesting(),
          WorkUnitsService,
          { provide: USE_MOCK_DATA, useValue: false },
        ],
      });

      return {
        service: get(WorkUnitsService),
        httpMock: get(HttpTestingController),
      };
    }, beforeEach);

    afterEach(() => {
      state.httpMock.verify();
    });

    it('should be instantiated', () => {
      expect(state.service).toBeTruthy();
    });

    it('should fetch work units from the server', async () => {
      const mockResponse = {
        workUnits: WORK_UNIT_LIST,
      };

      state.service.searchWorkUnits({ parent: 'experiments/1' }).subscribe((response) => {
        expect(response).toEqual(mockResponse);
      });

      const req = state.httpMock.expectOne(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/1/workUnits`,
      );
      expect(req.request.method).toBe('GET');
      req.flush(mockResponse);
    });

    it('should include filters and pagination in query params', async () => {
      state.service
        .searchWorkUnits({
          parent: 'experiments/1',
          pageSize: 10,
          pageToken: 'prev_token',
        })
        .subscribe();

      const req = state.httpMock.expectOne((req) => req.url.includes('/experiments/1/workUnits'));
      expect(req.request.params.get('pageSize')).toBe('10');
      expect(req.request.params.get('pageToken')).toBe('prev_token');
      req.flush({});
    });

    it('should fetch a single work unit', async () => {
      state.service.getWorkUnit({ name: 'experiments/1/workUnits/1' }).subscribe((response) => {
        expect(response).toEqual(WORK_UNIT_1);
      });

      const req = state.httpMock.expectOne(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/1/workUnits/1`,
      );
      expect(req.request.method).toBe('GET');
      req.flush(WORK_UNIT_1);
    });
  });

  describe('MockWorkUnitsService', () => {
    const state = cleanState(() => {
      setupModule({
        providers: [provideHttpClient(), MockWorkUnitsService],
      });

      return {
        service: get(MockWorkUnitsService),
      };
    }, beforeEach);

    it('should return mock work units', (done) => {
      state.service.searchWorkUnits({ parent: 'experiments/1' }).subscribe((response) => {
        expect(response.workUnits).toEqual(WORK_UNIT_LIST);
        expect(response.nextPageToken).toBe('page_token');
        done();
      });
    });

    it('should return mock single work unit', (done) => {
      state.service.getWorkUnit({ name: 'experiments/1/workUnits/1' }).subscribe((response) => {
        expect(response).toEqual(WORK_UNIT_1);
        done();
      });
    });
  });
});
