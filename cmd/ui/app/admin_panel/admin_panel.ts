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

import { ChangeDetectionStrategy, Component, computed, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { BehaviorSubject, of } from 'rxjs';
import { catchError, finalize, switchMap, tap } from 'rxjs/operators';

import {
  HealthStatusResponse,
  ListConfigEntriesResponse,
} from 'xmanager_cloud/health_service/proto/api.jsonpb_decls';

import { ErrorBannerComponent } from '../common/components/error_banner/error_banner';
import { HealthService } from '../service/health_service';
import { SnackbarService } from '../service/snackbar_service';
import { ConfigEntriesCardComponent } from './components/config_entries_card/config_entries_card';
import { HealthStatusSectionComponent } from './components/health_status_section/health_status_section';
import { SystemInfoCardComponent } from './components/system_info_card/system_info_card';

/**
 * Mock Admin Panel component for XMC.
 */
@Component({
  selector: 'xmc-admin-panel',
  standalone: true,
  imports: [
    MatProgressSpinnerModule,
    SystemInfoCardComponent,
    HealthStatusSectionComponent,
    ConfigEntriesCardComponent,
    ErrorBannerComponent,
  ],
  templateUrl: './admin_panel.ng.html',
  styleUrl: './admin_panel.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AdminPanelPage {
  private readonly healthService = inject(HealthService);
  private readonly snackbarService = inject(SnackbarService);

  protected readonly healthStatusError = signal<unknown>(null);
  protected readonly configEntriesError = signal<unknown>(null);
  protected readonly isConfigLoading = signal<boolean>(false);

  private lastKnownStatus: HealthStatusResponse | null = null;

  protected readonly isRefreshing = signal<boolean>(false);

  private readonly refreshTrigger$ = new BehaviorSubject<boolean>(false);
  private readonly configRefreshTrigger$ = new BehaviorSubject<void>(undefined);

  protected readonly healthStatus = toSignal(
    this.refreshTrigger$.pipe(
      tap(() => this.isRefreshing.set(true)),
      switchMap((forceRefresh) =>
        this.healthService.getHealthStatus(forceRefresh).pipe(
          tap((status) => {
            this.lastKnownStatus = status;
            this.healthStatusError.set(null);
          }),
          catchError((error) => {
            console.error('Failed to fetch health status', error);
            if (this.lastKnownStatus) {
              this.snackbarService.open('Failed to refresh health status.');
              return of(this.lastKnownStatus);
            }
            this.healthStatusError.set(error);
            return of({} as HealthStatusResponse);
          }),
          finalize(() => this.isRefreshing.set(false)),
        ),
      ),
    ),
  );

  protected readonly healthConfigs = toSignal(
    this.configRefreshTrigger$.pipe(
      tap(() => {
        this.isConfigLoading.set(true);
        this.configEntriesError.set(null);
      }),
      switchMap(() =>
        this.healthService.getHealthConfigs().pipe(
          catchError((error) => {
            console.error('Failed to fetch health configs', error);
            this.configEntriesError.set(error);
            return of({} as ListConfigEntriesResponse);
          }),
          finalize(() => this.isConfigLoading.set(false)),
        ),
      ),
    ),
  );

  protected readonly isLoading = computed(() => this.healthStatus() === undefined);

  protected readonly lastUpdated = computed(() => {
    const status = this.healthStatus();
    if (!status || !status.healthCheckTime) return '';
    const time = status.healthCheckTime;
    if (typeof time === 'string') {
      return new Date(time).toLocaleTimeString();
    }
    if (time.seconds) {
      return new Date(Number(time.seconds) * 1000).toLocaleTimeString();
    }
    return '';
  });

  protected onRefresh(): void {
    this.refreshTrigger$.next(true);
    this.configRefreshTrigger$.next();
  }

  protected onRetryConfigs(): void {
    this.configRefreshTrigger$.next();
  }
}
