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
import { HttpRequest, provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { firstValueFrom } from 'rxjs';

import { MOCK_HISTORICAL_STATUS_MESSAGES } from 'app/common/mock_data';
import { cleanState, get, setupModule, setupObservableMatchers } from 'app/testing/test';
import { ListStatusMessagesResponse } from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';

import { XMC_API_DOMAIN } from './constants';
import { StatusMessageService } from './status_message_service';

describe('StatusMessageService', () => {
  beforeAll(setupObservableMatchers);

  describe('with real data', () => {
    const state = cleanState(() => {
      setupModule({
        providers: [provideHttpClient(), provideHttpClientTesting(), StatusMessageService],
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

      const responsePromise = firstValueFrom(
        state.service.listStatusMessages({ parent: 'experiments/1' }),
      );

      const req = state.httpMock.expectOne(`${XMC_API_DOMAIN}/experiments/1/statusMessages`);
      expect(req.request.method).toBe('GET');
      req.flush(mockResponse);

      const response = await responsePromise;
      expect(response).toEqual(mockResponse);
    });

    it('should include filters and pagination in query params', async () => {
      const responsePromise = firstValueFrom(
        state.service.listStatusMessages({
          parent: 'experiments/1',
          pageSize: 10,
          pageToken: 'prev_token',
          filter: 'severity=ERROR',
        }),
      );

      const req = state.httpMock.expectOne((req: HttpRequest<unknown>) =>
        req.url.includes('/experiments/1/statusMessages'),
      );
      expect(req.request.params.get('pageSize')).toBe('10');
      expect(req.request.params.get('pageToken')).toBe('prev_token');
      expect(req.request.params.get('filter')).toBe('severity=ERROR');
      req.flush({});

      await responsePromise;
    });
  });
});
