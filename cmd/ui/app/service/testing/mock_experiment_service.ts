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
import { Injectable } from '@angular/core';
import { Observable, of, throwError } from 'rxjs';

import { EXPERIMENT_1, EXPERIMENT_LIST } from 'app/common/mock_data';
import {
  ListExperimentsRequest,
  ListExperimentsResponse,
  UpdateExperimentRequest,
} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import { Experiment } from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';

import { ExperimentService } from '../experiment_service';

/**
 * Mock implementation of ExperimentService for testing and mock data mode.
 */
@Injectable({ providedIn: 'root' })
export class MockExperimentService extends ExperimentService {
  override searchExperiments(request: ListExperimentsRequest): Observable<ListExperimentsResponse> {
    if (!request.pageToken) {
      return of({
        experiments: EXPERIMENT_LIST.slice(0, 4),
        nextPageToken: 'page_2',
      });
    }
    if (request.pageToken === 'page_2') {
      return of({
        experiments: EXPERIMENT_LIST.slice(3),
        nextPageToken: '',
      });
    }
    return throwError(() => new Error('Invalid page token'));
  }

  override getExperiment(xid: string): Observable<Experiment> {
    const result = EXPERIMENT_LIST.find((experiment) => experiment.name === `experiments/${xid}`);
    return of(result || EXPERIMENT_1);
  }

  override updateExperiment(request: UpdateExperimentRequest): Observable<Experiment> {
    if (!request.experiment) {
      return throwError(() => new Error('Experiment is required for update'));
    }
    return of(request.experiment);
  }
}
