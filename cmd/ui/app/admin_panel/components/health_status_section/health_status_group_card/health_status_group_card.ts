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

import { ComponentHealth, HealthState } from '../health_status_section';

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

  protected readonly isDegraded = computed(() => {
    const comps = this.components();
    if (comps.length === 0) return false;
    return comps.some((c) => c.healthState === HealthState.UNHEALTHY);
  });

  protected readonly groupStatusText = computed(() => {
    const comps = this.components();
    if (comps.length === 0) return 'UNKNOWN';
    const hasUnhealthy = comps.some((c) => c.healthState === HealthState.UNHEALTHY);
    return hasUnhealthy ? 'DEGRADED' : 'HEALTHY';
  });

  protected mapHealthState(state?: string): string {
    if (state === HealthState.HEALTHY) return 'HEALTHY';
    if (state === HealthState.UNHEALTHY) return 'DEGRADED';
    return 'UNKNOWN';
  }
}
