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
import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';

import { ComponentHealth, ComponentType, HealthState } from '../health_status_section';

/** Extended component health item with precomputed status label. */
export interface ComponentHealthItem extends ComponentHealth {
  statusLabel: string;
}

/** Component representing a single grouped health card (e.g., Core, GCP, Custom). */
@Component({
  selector: 'xmc-health-status-group-card',
  standalone: true,
  imports: [CommonModule, MatIconModule],
  templateUrl: './health_status_group_card.ng.html',
  styleUrl: './health_status_group_card.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class HealthStatusGroupCardComponent {
  title = input.required<string>();
  icon = input.required<string>();
  components = input.required<ComponentHealth[]>();

  protected readonly HealthState = HealthState;

  protected readonly isDegraded = computed(() => {
    return this.components().some((c) => c.healthState === HealthState.UNHEALTHY);
  });

  protected readonly isHealthy = computed(() => {
    return (
      this.components().length > 0 &&
      this.components().every((c) => c.healthState === HealthState.HEALTHY)
    );
  });

  protected readonly isUnknown = computed(() => {
    return !this.isDegraded() && !this.isHealthy();
  });

  protected readonly groupStatusText = computed(() => {
    if (this.isDegraded()) {
      return 'DEGRADED';
    }
    if (this.isHealthy()) {
      return 'HEALTHY';
    }
    return 'UNKNOWN';
  });

  protected readonly items = computed<ComponentHealthItem[]>(() => {
    return this.components().map((item) => ({
      ...item,
      statusLabel: this.calculateStatusLabel(item),
    }));
  });

  private calculateStatusLabel(item: ComponentHealth): string {
    if (item.healthState === HealthState.HEALTHY) {
      if (item.type === ComponentType.CORE_SERVICE) return 'Running';
      if (item.type === ComponentType.EXTERNAL_DEPENDENCY) return 'Online';
      if (item.type === ComponentType.CUSTOM_COMPONENT) return 'Connected';
      return 'Running';
    }
    if (item.healthState === HealthState.UNHEALTHY) {
      return 'Degraded';
    }
    return 'Unknown';
  }
}
