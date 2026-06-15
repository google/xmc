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
import { Injectable } from '@angular/core';
import { Observable, of } from 'rxjs';
import {
  ListStatusMessagesRequest,
  ListStatusMessagesResponse,
} from 'xmanager_cloud/experiment_state_server/proto/api.jsonpb_decls';
import { MOCK_HISTORICAL_STATUS_MESSAGES } from 'xmanager_cloud/ui/app/common/mock_data';

import { StatusMessageService } from '../status_message_service';

/**
 * Mock implementation of StatusMessageService for testing and mock data mode.
 */
@Injectable({ providedIn: 'root' })
export class MockStatusMessageService extends StatusMessageService {
  override listStatusMessages(
    request: ListStatusMessagesRequest,
  ): Observable<ListStatusMessagesResponse> {
    return of({
      statusMessages: MOCK_HISTORICAL_STATUS_MESSAGES,
      nextPageToken: 'page_token',
    });
  }
}
