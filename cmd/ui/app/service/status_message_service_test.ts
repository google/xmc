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
import { cleanState, setupModule, get, flush, setupObservableMatchers } from '../testing/test';
import 'jasmine';

import {provideHttpClient} from '@angular/common/http';
import {HttpTestingController, provideHttpClientTesting} from '@angular/common/http/testing';
import {ListStatusMessagesResponse} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import {MOCK_HISTORICAL_STATUS_MESSAGES} from 'xmanager_cloud/ui/app/common/mock_data';

import {EXPERIMENT_STATE_SERVER_DOMAIN, USE_MOCK_DATA} from './constants';
import {StatusMessageService} from './status_message_service';

describe('StatusMessageService', () => {
  beforeAll(setupObservableMatchers);

  describe('with real data', () => {
    const state = cleanState(() => {
      setupModule({
        providers: [
          provideHttpClient(),
          provideHttpClientTesting(),
          StatusMessageService,
          {provide: USE_MOCK_DATA, useValue: false},
        ],
      });

      return {
        service: get(StatusMessageService),
        httpMock: get(HttpTestingController),
      };
    }, beforeEach);

    afterEach(() => {
      state.httpMock.verify();
    });

    it('should fetch status messages from the server', async () => {
      const mockResponse: ListStatusMessagesResponse = {
        statusMessages: MOCK_HISTORICAL_STATUS_MESSAGES,
        nextPageToken: 'token',
      };

      state.service.listStatusMessages({parent: 'experiments/1'})
          .subscribe(response => {
            expect(response).toEqual(mockResponse);
          });

      const req = state.httpMock.expectOne(
          `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/1/statusMessages`);
      expect(req.request.method).toBe('GET');
      req.flush(mockResponse);
    });

    it('should include filters and pagination in query params', async () => {
      state.service
          .listStatusMessages({
            parent: 'experiments/1',
            pageSize: 10,
            pageToken: 'prev_token',
            filter: 'severity=ERROR',
          })
          .subscribe();

      const req = state.httpMock.expectOne(
          req => req.url.includes('/experiments/1/statusMessages'));
      expect(req.request.params.get('pageSize')).toBe('10');
      expect(req.request.params.get('pageToken')).toBe('prev_token');
      expect(req.request.params.get('filter')).toBe('severity=ERROR');
      req.flush({});
    });
  });

  describe('with mock data', () => {
    const state = cleanState(() => {
      setupModule({
        providers: [
          provideHttpClient(),
          StatusMessageService,
          {provide: USE_MOCK_DATA, useValue: true},
        ],
      });

      return {
        service: get(StatusMessageService),
      };
    }, beforeEach);

    it('should return mock status messages', (done) => {
      state.service.listStatusMessages({parent: 'experiments/1'})
          .subscribe(response => {
            expect(response.statusMessages)
                .toEqual(MOCK_HISTORICAL_STATUS_MESSAGES);
            expect(response.nextPageToken).toBe('page_token');
            done();
          });
    });
  });
});
