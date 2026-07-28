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

import { HttpClient } from '@angular/common/http';
import { inject, Injectable } from '@angular/core';
import { Observable, of } from 'rxjs';
import { catchError, map, shareReplay } from 'rxjs/operators';

import {
  HealthStatusResponse,
  ListConfigEntriesResponse,
  UserInfoResponse,
} from 'xmanager_cloud/health_service/proto/api.jsonpb_decls';

import { EXPERIMENT_STATE_SERVER_DOMAIN } from './constants';
import { FeatureFlagService } from './feature_flag_service';

/** Privilege required to access the admin panel. */
export const PRIVILEGE_ACCESS_ADMIN_PANEL = 'PRIVILEGE_ACCESS_ADMIN_PANEL';

/**
 * Service to fetch health status and configuration.
 */
@Injectable({ providedIn: 'root' })
export class HealthService {
  private readonly http = inject(HttpClient);
  private readonly featureFlagService = inject(FeatureFlagService);

  getHealthStatus(): Observable<HealthStatusResponse> {
    return this.http.get<HealthStatusResponse>(`${EXPERIMENT_STATE_SERVER_DOMAIN}/health/status`, {
      withCredentials: true,
    });
  }

  getHealthConfigs(): Observable<ListConfigEntriesResponse> {
    return this.http.get<ListConfigEntriesResponse>(
      `${EXPERIMENT_STATE_SERVER_DOMAIN}/health/configs`,
      {
        withCredentials: true,
      },
    );
  }

  private userInfo$?: Observable<UserInfoResponse | null>;

  getUserInfo(): Observable<UserInfoResponse | null> {
    if (!this.userInfo$) {
      this.userInfo$ = this.http
        .get<UserInfoResponse>(`${EXPERIMENT_STATE_SERVER_DOMAIN}/health/userinfo`, {
          withCredentials: true,
        })
        .pipe(
          catchError((error) => {
            console.error('Failed to fetch user info', error);
            return of(null);
          }),
          shareReplay(1),
        );
    }
    return this.userInfo$;
  }

  isAdminPanelAccessible(): Observable<boolean> {
    if (!this.featureFlagService.isAdminPanelEnabled) {
      return of(false);
    }
    return this.getUserInfo().pipe(
      map((info) => !!info?.privileges?.includes(PRIVILEGE_ACCESS_ADMIN_PANEL)),
    );
  }
}
