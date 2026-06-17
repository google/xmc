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
import { HttpClient, HttpParams } from '@angular/common/http';
import { inject, Injectable } from '@angular/core';
import { Observable, of } from 'rxjs';

import {
  ListArtifactsRequest,
  ListArtifactsResponse,
} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import { ARTIFACT_LIST, WORK_UNIT_1_ARTIFACT_LIST } from 'xmanager_cloud/ui/app/common/mock_data';

import { EXPERIMENT_STATE_SERVER_DOMAIN, USE_MOCK_DATA } from './constants';

const PAGE_SIZE = 950;

/**
 * Service to fetch artifacts.
 */
@Injectable({ providedIn: 'root' })
export class ArtifactService {
  private readonly http = inject(HttpClient);
  private readonly useMockData = inject(USE_MOCK_DATA);

  /**
   * Fetch all the artifacts for a resource.
   */
  listArtifacts(request: ListArtifactsRequest): Observable<ListArtifactsResponse> {
    if (this.useMockData) {
      const artifacts = request.parent?.includes('workUnits')
        ? WORK_UNIT_1_ARTIFACT_LIST
        : ARTIFACT_LIST;
      const result: ListArtifactsResponse = {
        artifacts,
        nextPageToken: 'page_token',
      };
      return of(result);
    }

    return this.listArtifactsPage(request);
  }

  private listArtifactsPage(request: ListArtifactsRequest): Observable<ListArtifactsResponse> {
    const { parent, filter, orderBy, pageToken } = request;
    let params = new HttpParams();
    // By default, let's try to get all of the artifacts. For now, the server
    // will cap it at 950.
    params = params.set('pageSize', PAGE_SIZE);
    if (filter) {
      params = params.set('filter', filter);
    }
    if (orderBy) {
      params = params.set('orderBy', orderBy);
    } else {
      params = params.set('orderBy', 'priority');
    }
    if (pageToken) {
      params = params.set('pageToken', pageToken);
    }
    return this.http.get<ListArtifactsResponse>(
      `${EXPERIMENT_STATE_SERVER_DOMAIN}/${parent}/artifacts:search`,
      { params, withCredentials: true },
    );
  }
}
