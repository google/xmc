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
  ListStatusMessagesRequest,
  ListStatusMessagesResponse,
} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';

import { XMC_API_DOMAIN } from './constants';

/**
 * Service to fetch status messages.
 */
@Injectable({ providedIn: 'root' })
export class StatusMessageService {
  private readonly http = inject(HttpClient);

  /** List status messages for a given parent (experiment or work unit). */
  listStatusMessages(request: ListStatusMessagesRequest): Observable<ListStatusMessagesResponse> {
    const { parent, pageSize, pageToken, filter, orderBy } = request;
    const params = new HttpParams({
      fromObject: Object.fromEntries(
        Object.entries({ pageSize, pageToken, filter, orderBy }).filter(([_, v]) => v != null),
      ),
    });

    return this.http.get<ListStatusMessagesResponse>(`${XMC_API_DOMAIN}/${parent}/statusMessages`, {
      params,
      withCredentials: true,
    });
  }
}
