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

import { CommonModule } from '@angular/common';
import { ChangeDetectionStrategy, Component, computed, input, output, signal } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';

import { HealthStatusGroupCardComponent } from './health_status_group_card/health_status_group_card';
import { SchedulingPauseButtonComponent } from './scheduling_pause_button/scheduling_pause_button';
import { SchedulingPauseModalComponent } from './scheduling_pause_modal/scheduling_pause_modal';

/** Enum representing the component types from backend health service. */
export enum ComponentType {
  UNSPECIFIED = 'COMPONENT_TYPE_UNSPECIFIED',
  CORE_SERVICE = 'COMPONENT_TYPE_CORE_SERVICE',
  EXTERNAL_DEPENDENCY = 'COMPONENT_TYPE_EXTERNAL_DEPENDENCY',
  CUSTOM_COMPONENT = 'COMPONENT_TYPE_CUSTOM_COMPONENT',
}

/** Enum representing the health state states from backend health service. */
export enum HealthState {
  UNSPECIFIED = 'HEALTH_STATE_UNSPECIFIED',
  HEALTHY = 'HEALTH_STATE_HEALTHY',
  UNHEALTHY = 'HEALTH_STATE_UNHEALTHY',
}

/** Interface representing component health information from backend response. */
export interface ComponentHealth {
  name?: string;
  type?: string;
  healthState?: string;
  errorMessage?: string;
}

/** Component representing XMC services health details. */
@Component({
  selector: 'xmc-health-status-section',
  standalone: true,
  imports: [
    CommonModule,
    MatIconModule,
    HealthStatusGroupCardComponent,
    SchedulingPauseButtonComponent,
    SchedulingPauseModalComponent,
  ],
  templateUrl: './health_status_section.ng.html',
  styleUrl: './health_status_section.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class HealthStatusSectionComponent {
  components = input.required<ComponentHealth[]>();
  overallStatus = input<string>(HealthState.UNSPECIFIED);
  lastChecked = input<string>('');
  loading = input<boolean>(false);
  isSchedulingPaused = input<boolean>(false);
  isPauseSchedulingEnabled = input<boolean>(true);
  isUpdatingPauseState = input<boolean>(false);

  refresh = output<void>();
  toggleSchedulingPause = output<boolean>();

  protected readonly showPauseModal = signal<boolean>(false);
  protected readonly pendingPauseState = signal<boolean>(false);

  // Computed properties to group components
  protected readonly coreComponents = computed(() =>
    this.components().filter((c) => c.type === ComponentType.CORE_SERVICE),
  );

  protected readonly gcpComponents = computed(() =>
    this.components().filter((c) => c.type === ComponentType.EXTERNAL_DEPENDENCY),
  );

  protected readonly customComponents = computed(() =>
    this.components().filter((c) => c.type === ComponentType.CUSTOM_COMPONENT),
  );

  protected readonly isHealthy = computed(() => this.overallStatus() === HealthState.HEALTHY);
  protected readonly isDegraded = computed(() => this.overallStatus() === HealthState.UNHEALTHY);
  protected readonly isUnknown = computed(() => this.overallStatus() === HealthState.UNSPECIFIED);

  protected mapHealthState(state?: string): string {
    if (state === HealthState.HEALTHY) return 'HEALTHY';
    if (state === HealthState.UNHEALTHY) return 'DEGRADED';
    return 'UNKNOWN';
  }

  protected onRefresh(): void {
    this.refresh.emit();
  }

  protected onTogglePause(): void {
    this.pendingPauseState.set(!this.isSchedulingPaused());
    this.showPauseModal.set(true);
  }

  protected onConfirmPause(): void {
    const targetState = this.pendingPauseState();
    this.showPauseModal.set(false);
    this.toggleSchedulingPause.emit(targetState);
  }

  protected onCancelPause(): void {
    this.showPauseModal.set(false);
  }
}
