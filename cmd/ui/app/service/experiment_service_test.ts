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

import {provideHttpClient} from '@angular/common/http';
import {HttpTestingController, provideHttpClientTesting} from '@angular/common/http/testing';
import {ListExperimentsRequest, ListExperimentsResponse} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';

import {EXPERIMENT_1} from '../common/mock_data';

import {EXPERIMENT_STATE_SERVER_DOMAIN, USE_MOCK_DATA} from './constants';
import {ExperimentService} from './experiment_service';

describe('ExperimentService Service', () => {
  beforeAll(setupObservableMatchers);

  const state = cleanState(() => {
    setupModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        ExperimentService,
        {provide: USE_MOCK_DATA, useValue: false},
      ],
    });

    return {
      service: get(ExperimentService),
      httpMock: get(HttpTestingController),
    };
  }, beforeEach);

  afterEach(() => {
    state.httpMock.verify();
  });

  describe('searchExperiments', () => {
    const mockResponse: ListExperimentsResponse = {
      experiments: [
        EXPERIMENT_1,
        {...EXPERIMENT_1, name: 'experiments/2'},
        {...EXPERIMENT_1, name: 'experiments/3'},
        {...EXPERIMENT_1, name: 'experiments/4'},
        {...EXPERIMENT_1, name: 'experiments/5'},
      ],
      nextPageToken: 'page_2',
    };

    it('should make api call correctly', async () => {
      const request: ListExperimentsRequest = {
        filter: 'user:me',
        pageSize: 10,
        pageToken: 'page_1',
      };
      state.service.searchExperiments(request).subscribe((response) => {
        expect(response).toEqual(mockResponse);
      });

      const req = state.httpMock.expectOne(
          `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments?pageSize=10&pageToken=page_1&filter=user:me&orderBy=created%20desc`,
      );
      expect(req.request.method).toBe('GET');
      expect(req.request.params.get('filter')).toBe('user:me');
      expect(req.request.params.get('pageSize')).toBe('10');
      expect(req.request.params.get('pageToken')).toBe('page_1');
      expect(req.request.params.get('orderBy')).toBe('created desc');

      req.flush(mockResponse);
    });
  });

  describe('getExperiment', () => {
    it('should make api call correctly', async () => {
      const xid = '1';
      state.service.getExperiment(xid).subscribe((response) => {
        expect(response).toEqual(EXPERIMENT_1);
      });

      const req = state.httpMock.expectOne(
          `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/${xid}`,
      );
      expect(req.request.method).toBe('GET');

      req.flush(EXPERIMENT_1);
    });
  });
});
