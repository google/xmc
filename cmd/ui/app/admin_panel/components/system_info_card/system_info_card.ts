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

import { ChangeDetectionStrategy, Component, computed, input } from '@angular/core';
import { MatIconModule } from '@angular/material/icon';

import { HealthStatusResponse } from 'app/protos/health_service';

export type SystemInfo = NonNullable<HealthStatusResponse['systemInfo']>;

export const UNAVAILABLE_SCHEMA_VERSION = 'UNAVAILABLE';
export const UNKNOWN_VALUE = 'unknown';
const PLACEHOLDER_VERSION = '-';

/** A component showing system version info and external monitoring links. */
@Component({
  selector: 'xmc-system-info-card',
  standalone: true,
  imports: [MatIconModule],
  templateUrl: './system_info_card.ng.html',
  styleUrl: './system_info_card.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class SystemInfoCardComponent {
  systemInfo = input.required<SystemInfo>();

  protected readonly imageTag = computed(() =>
    this.formatValue(this.systemInfo().imageTag, UNKNOWN_VALUE),
  );
  protected readonly isImageTagPlaceholder = computed(
    () => !this.isValueAvailable(this.systemInfo().imageTag, UNKNOWN_VALUE),
  );

  protected readonly essDbSchemaVersion = computed(() =>
    this.formatValue(this.systemInfo().essDbSchemaVersion, UNAVAILABLE_SCHEMA_VERSION),
  );
  protected readonly xidDbSchemaVersion = computed(() =>
    this.formatValue(this.systemInfo().xidDbSchemaVersion, UNAVAILABLE_SCHEMA_VERSION),
  );

  protected readonly isEssUnavailable = computed(
    () => !this.isValueAvailable(this.systemInfo().essDbSchemaVersion, UNAVAILABLE_SCHEMA_VERSION),
  );
  protected readonly isXidUnavailable = computed(
    () => !this.isValueAvailable(this.systemInfo().xidDbSchemaVersion, UNAVAILABLE_SCHEMA_VERSION),
  );

  private formatValue(value: string | undefined | null, sentinel: string): string {
    return this.isValueAvailable(value, sentinel) ? value : PLACEHOLDER_VERSION;
  }

  private isValueAvailable(value: string | undefined | null, sentinel: string): value is string {
    return Boolean(value) && value !== sentinel;
  }
}
