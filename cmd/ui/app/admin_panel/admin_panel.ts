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

import { JsonPipe } from '@angular/common';
import { ChangeDetectionStrategy, Component, computed, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import {
  HealthStatusResponse,
  ListConfigEntriesResponse,
} from 'xmanager_cloud/health_service/proto/api.jsonpb_decls';

import { HealthService } from '../service/health_service';
import { SystemInfoCardComponent } from './components/system_info_card/system_info_card';

/**
 * Mock Admin Panel component for XMC.
 */
@Component({
  selector: 'xmc-admin-panel',
  standalone: true,
  imports: [JsonPipe, SystemInfoCardComponent],
  templateUrl: './admin_panel.ng.html',
  styleUrl: './admin_panel.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AdminPanelPage {
  private readonly healthService = inject(HealthService);

  protected readonly healthStatusError = signal<unknown>(null);

  protected readonly healthStatus = toSignal(
    this.healthService.getHealthStatus().pipe(
      catchError((error) => {
        console.error('Failed to fetch health status', error);
        this.healthStatusError.set(error);
        return of({} as HealthStatusResponse);
      }),
    ),
  );

  protected readonly healthConfigs = toSignal(
    this.healthService.getHealthConfigs().pipe(
      catchError((error) => {
        console.error('Failed to fetch health configs', error);
        return of({} as ListConfigEntriesResponse);
      }),
    ),
  );

  protected readonly isLoading = computed(() => this.healthStatus() === undefined);

  protected readonly lastUpdated = computed(() => {
    const status = this.healthStatus();
    if (!status || !status.healthCheckTime) return '';
    const time = status.healthCheckTime;
    if (time.seconds) {
      return new Date(Number(time.seconds) * 1000).toLocaleTimeString();
    }
    if (typeof time === 'string') {
      return new Date(time).toLocaleTimeString();
    }
    return '';
  });
}
