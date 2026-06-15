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
  ListExperimentsRequest,
  ListExperimentsResponse,
  UpdateExperimentRequest,
} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import { Experiment } from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';

import { EXPERIMENT_STATE_SERVER_DOMAIN } from './constants';

/**
 * Service to fetch experiments.
 */
@Injectable({ providedIn: 'root' })
export class ExperimentService {
  private readonly http = inject(HttpClient);

  /** Search for experiments. */
  searchExperiments(request: ListExperimentsRequest): Observable<ListExperimentsResponse> {
    const { pageSize, pageToken, filter, orderBy } = request;
    const paramsMap: Record<string, string | number | boolean> = {};
    if (pageSize) {
      paramsMap['pageSize'] = pageSize;
    }
    if (pageToken) {
      paramsMap['pageToken'] = pageToken;
    }
    if (filter) {
      paramsMap['filter'] = filter;
    }
    paramsMap['orderBy'] = orderBy || 'created desc';

    const params = new HttpParams({ fromObject: paramsMap });

    return this.http.get<ListExperimentsResponse>(`${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments`, {
      params,
      withCredentials: true,
    });
  }

  /**
   * Fetch an experiment from the experiment state server.
   */
  getExperiment(xid: string): Observable<Experiment> {
    return this.http.get<Experiment>(`${EXPERIMENT_STATE_SERVER_DOMAIN}/experiments/${xid}`, {
      withCredentials: true,
    });
  }

  /**
   * Updates an experiment.
   * It returns a new experiment with updated fields.
   */
  updateExperiment(request: UpdateExperimentRequest): Observable<Experiment> {
    const paramsMap: Record<string, string> = {};
    if (request.updateMask) {
      paramsMap['updateMask'] = request.updateMask;
    }
    const params = new HttpParams({ fromObject: paramsMap });
    return this.http.patch<Experiment>(
      `${EXPERIMENT_STATE_SERVER_DOMAIN}/${request.experiment!.name}`,
      request.experiment,
      { params, withCredentials: true },
    );
  }
}
