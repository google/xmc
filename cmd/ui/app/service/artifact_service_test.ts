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

import {
  ListArtifactsRequest,
  ListArtifactsResponse,
} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';

import { ARTIFACT_LIST } from '../common/mock_data';
import { cleanState, flush, get, setupModule, setupObservableMatchers } from '../testing/test';
import { ArtifactService } from './artifact_service';
import { EXPERIMENT_STATE_SERVER_DOMAIN, USE_MOCK_DATA } from './constants';
import { MockArtifactService } from './testing/mock_artifact_service';

describe('ArtifactService Service', () => {
  beforeAll(setupObservableMatchers);

  const state = cleanState(() => {
    setupModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        ArtifactService,
        { provide: USE_MOCK_DATA, useValue: false },
      ],
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

      state.service.listArtifacts(request).subscribe((response) => {
        expect(response).toEqual(expectedResponse);
      });

      const req = state.httpMock.expectOne(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/1/artifacts:search?pageSize=950&filter=foo&orderBy=bar`,
      );
      expect(req.request.method).toBe('GET');
      expect(req.request.params.get('pageSize')).toBe('950');
      expect(req.request.params.get('filter')).toBe('foo');
      expect(req.request.params.get('orderBy')).toBe('bar');
      expect(req.request.params.get('pageToken')).toBeNull();
      req.flush(page1Response);
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

      state.service.listArtifacts(request).subscribe((response) => {
        expect(response).toEqual(expectedResponse);
      });

      const req = state.httpMock.expectOne(
        `${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/1/artifacts:search?pageSize=950&filter=foo&orderBy=priority`,
      );
      expect(req.request.method).toBe('GET');
      expect(req.request.params.get('pageSize')).toBe('950');
      expect(req.request.params.get('filter')).toBe('foo');
      expect(req.request.params.get('orderBy')).toBe('priority');
      expect(req.request.params.get('pageToken')).toBeNull();
      req.flush(page1Response);
    });
  });
});

describe('MockArtifactService', () => {
  const mockState = cleanState(() => {
    setupModule({
      providers: [provideHttpClient(), MockArtifactService],
    });

    return {
      service: get(MockArtifactService),
    };
  }, beforeEach);

  it('should return mock artifacts', (done) => {
    mockState.service.listArtifacts({ parent: 'experiments/1' }).subscribe((response) => {
      expect(response.artifacts).toEqual(ARTIFACT_LIST);
      expect(response.nextPageToken).toBe('page_token');
      done();
    });
  });
});
