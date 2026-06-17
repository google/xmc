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
import { Observable } from 'rxjs';

import {
  ListArtifactsRequest,
  ListArtifactsResponse,
} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';

import { EXPERIMENT_STATE_SERVER_DOMAIN } from './constants';

const PAGE_SIZE = 950;

/**
 * Service to fetch artifacts.
 */
@Injectable({ providedIn: 'root' })
export class ArtifactService {
  private readonly http = inject(HttpClient);

  /**
   * Fetch all the artifacts for a resource.
   */
  listArtifacts(request: ListArtifactsRequest): Observable<ListArtifactsResponse> {
    const { parent, filter, orderBy, pageToken } = request;
    const paramsMap: Record<string, string | number | boolean> = {
      pageSize: PAGE_SIZE,
    };
    if (filter) {
      paramsMap['filter'] = filter;
    }
    paramsMap['orderBy'] = orderBy || 'priority';
    if (pageToken) {
      paramsMap['pageToken'] = pageToken;
    }
    const params = new HttpParams({ fromObject: paramsMap });

    return this.http.get<ListArtifactsResponse>(
      `${EXPERIMENT_STATE_SERVER_DOMAIN}/${parent}/artifacts:search`,
      { params, withCredentials: true },
    );
  }
}
