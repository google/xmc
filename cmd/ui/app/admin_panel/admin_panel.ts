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

import {
  ChangeDetectionStrategy,
  Component,
  computed,
  DestroyRef,
  inject,
  signal,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { finalize } from 'rxjs/operators';

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
  private readonly destroyRef = inject(DestroyRef);

  // Health Status state (Fetchable, Refreshable, Stale-Preserving on error)
  protected readonly healthStatus = signal<HealthStatusResponse | null>(null);
  protected readonly healthStatusError = signal<unknown>(null);
  protected readonly isLoading = signal<boolean>(true);
  protected readonly isRefreshing = signal<boolean>(false);

  // Config Entries state (Fetched once on page load, retriable only on error)
  protected readonly healthConfigs = signal<ListConfigEntriesResponse | null>(null);
  protected readonly configEntriesError = signal<unknown>(null);
  protected readonly isConfigLoading = signal<boolean>(true);

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

  constructor() {
    this.loadHealthStatus(false);
    this.loadHealthConfigs();
  }

  protected onRefresh(): void {
    this.loadHealthStatus(true);
  }

  protected onRetry(): void {
    this.healthStatusError.set(null);
    this.loadHealthStatus(false);
  }

  protected onRetryConfigs(): void {
    this.loadHealthConfigs();
  }

  private loadHealthStatus(forceRefresh = false): void {
    if (forceRefresh) {
      this.isRefreshing.set(true);
    } else {
      this.isLoading.set(true);
    }

    this.healthService
      .getHealthStatus(forceRefresh)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => {
          this.isLoading.set(false);
          this.isRefreshing.set(false);
        }),
      )
      .subscribe({
        next: (status) => {
          this.healthStatus.set(status);
          this.healthStatusError.set(null);
        },
        error: (error) => {
          console.error('Failed to fetch health status', error);
          if (this.healthStatus()) {
            this.snackbarService.open('Failed to refresh health status.');
          } else {
            this.healthStatusError.set(error);
          }
        },
      });
  }

  private loadHealthConfigs(): void {
    this.isConfigLoading.set(true);
    this.configEntriesError.set(null);

    this.healthService
      .getHealthConfigs()
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => this.isConfigLoading.set(false)),
      )
      .subscribe({
        next: (configs) => {
          this.healthConfigs.set(configs);
          this.configEntriesError.set(null);
        },
        error: (error) => {
          console.error('Failed to fetch health configs', error);
          this.healthConfigs.set(null);
          this.configEntriesError.set(error);
        },
      });
  }
}
