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
import { takeUntilDestroyed, toSignal } from '@angular/core/rxjs-interop';
import { MatIconModule } from '@angular/material/icon';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { BehaviorSubject, of } from 'rxjs';
import { catchError, finalize, switchMap, tap } from 'rxjs/operators';

import { HealthStatusResponse, ListConfigEntriesResponse } from 'app/protos/health_service';

import { ErrorBannerComponent } from '../common/components/error_banner/error_banner';
import { FeatureFlagService } from '../service/feature_flag_service';
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
    MatIconModule,
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
  private readonly featureFlagService = inject(FeatureFlagService);
  private readonly destroyRef = inject(DestroyRef);

  protected readonly healthStatusError = signal<unknown>(null);
  protected readonly configEntriesError = signal<unknown>(null);
  protected readonly isConfigLoading = signal<boolean>(false);

  private lastKnownStatus: HealthStatusResponse | null = null;

  protected readonly isRefreshing = signal<boolean>(false);
  protected readonly isUpdatingPauseState = signal<boolean>(false);
  protected readonly localSchedulingPaused = signal<boolean | null>(null);

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
            this.localSchedulingPaused.set(null);
          }),
          catchError((error) => {
            console.error('Failed to fetch health status', error);
            if (this.lastKnownStatus) {
              this.snackbarService.open('Failed to refresh health status.');
              return of(this.lastKnownStatus);
            }
            this.healthStatusError.set(error);
            return of(undefined);
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
          tap(() => {
            this.isConfigLoading.set(false);
          }),
          catchError((error) => {
            console.error('Failed to fetch health configs', error);
            this.isConfigLoading.set(false);
            this.configEntriesError.set(error);
            return of({} as ListConfigEntriesResponse);
          }),
        ),
      ),
    ),
  );

  protected readonly isLoading = computed(
    () => this.healthStatus() === undefined && !this.healthStatusError(),
  );

  protected readonly isSchedulingPauseError = computed(() =>
    Boolean(this.healthStatus()?.systemInfo?.schedulingPauseError),
  );

  protected readonly isSchedulingPaused = computed(() => {
    if (this.localSchedulingPaused() !== null) {
      return this.localSchedulingPaused()!;
    }
    return !!this.healthStatus()?.systemInfo?.isSchedulingPaused;
  });

  protected readonly isPauseSchedulingEnabled = computed(
    () => this.featureFlagService.isPauseSchedulingEnabled,
  );

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

  protected onRetry(): void {
    this.healthStatusError.set(null);
    this.refreshTrigger$.next(true);
  }

  protected onRefresh(): void {
    this.refreshTrigger$.next(true);
  }

  protected onRetryConfigs(): void {
    this.configRefreshTrigger$.next();
  }

  protected onToggleSchedulingPause(pause: boolean): void {
    if (
      !this.isPauseSchedulingEnabled() ||
      this.isUpdatingPauseState() ||
      this.isSchedulingPauseError()
    ) {
      return;
    }

    this.isUpdatingPauseState.set(true);
    const request$ = pause
      ? this.healthService.pauseScheduling()
      : this.healthService.resumeScheduling();

    request$
      .pipe(
        finalize(() => this.isUpdatingPauseState.set(false)),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe({
        next: () => {
          this.localSchedulingPaused.set(pause);
          this.snackbarService.open(
            pause ? 'All scheduling has been paused.' : 'Scheduling has been resumed.',
          );
        },
        error: (error) => {
          console.error('Failed to update scheduling pause state', error);
          this.snackbarService.open(
            pause ? 'Failed to pause scheduling.' : 'Failed to resume scheduling.',
          );
        },
      });
  }
}
