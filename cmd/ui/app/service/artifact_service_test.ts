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
import { firstValueFrom } from 'rxjs';

import {
  ListArtifactsRequest,
  ListArtifactsResponse,
} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';

import { ARTIFACT_LIST } from '../common/mock_data';
import { cleanState, get, setupModule, setupObservableMatchers } from '../testing/test';
import { ArtifactService } from './artifact_service';
import { EXPERIMENT_STATE_SERVER_DOMAIN } from './constants';

describe('ArtifactService Service', () => {
  beforeAll(setupObservableMatchers);

  const state = cleanState(() => {
    setupModule({
      providers: [provideHttpClient(), provideHttpClientTesting(), ArtifactService],
    });

    return {
      service: get(ArtifactService),
      httpMock: get(HttpTestingController),
    };
  }, beforeEach);

  afterEach(() => {
    state.httpMock.verify();
  });

  describe('listArtifacts', () => {
    it('should respect filter and orderBy', async () => {
      const request: ListArtifactsRequest = {
        parent: 'experiments/1',
        filter: 'foo',
        orderBy: 'bar',
      };
      const artifacts = [ARTIFACT_LIST[0]];
      const page1Response: ListArtifactsResponse = {
        artifacts,
      };
      const expectedResponse: ListArtifactsResponse = {
        artifacts,
      };

      const responsePromise = firstValueFrom(state.service.listArtifacts(request));

      const req = state.httpMock.expectOne(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/1/artifacts:search?pageSize=950&filter=foo&orderBy=bar`,
      );
      expect(req.request.method).toBe('GET');
      expect(req.request.params.get('pageSize')).toBe('950');
      expect(req.request.params.get('filter')).toBe('foo');
      expect(req.request.params.get('orderBy')).toBe('bar');
      expect(req.request.params.get('pageToken')).toBeNull();
      req.flush(page1Response);

      const response = await responsePromise;
      expect(response).toEqual(expectedResponse);
    });

    it('should default orderBy to priority when not provided', async () => {
      const request: ListArtifactsRequest = {
        parent: 'experiments/1',
        filter: 'foo',
      };
      const artifacts = [ARTIFACT_LIST[0]];
      const page1Response: ListArtifactsResponse = {
        artifacts,
      };
      const expectedResponse: ListArtifactsResponse = {
        artifacts,
      };

      const responsePromise = firstValueFrom(state.service.listArtifacts(request));

      const req = state.httpMock.expectOne(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/1/artifacts:search?pageSize=950&filter=foo&orderBy=priority`,
      );
      expect(req.request.method).toBe('GET');
      expect(req.request.params.get('pageSize')).toBe('950');
      expect(req.request.params.get('filter')).toBe('foo');
      expect(req.request.params.get('orderBy')).toBe('priority');
      expect(req.request.params.get('pageToken')).toBeNull();
      req.flush(page1Response);

      const response = await responsePromise;
      expect(response).toEqual(expectedResponse);
    });
  });
});
