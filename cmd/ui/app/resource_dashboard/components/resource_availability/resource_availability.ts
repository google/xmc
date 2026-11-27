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
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';

import { ErrorBannerComponent } from 'app/common/components/error_banner/error_banner';
import { AcceleratorSummary, GetResourceAvailabilityResponse } from 'app/protos/rod';
import { ResourceService } from 'app/service/resource_service';

/**
 * Displays physical GPU availability grouped by accelerator type, zone, and reservation,
 * along with correlated Kueue ClusterQueues.
 */
@Component({
  selector: 'xmc-rod-resource-availability',
  standalone: true,
  imports: [ErrorBannerComponent, MatButtonModule, MatIconModule],
  templateUrl: './resource_availability.ng.html',
  styleUrl: './resource_availability.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ResourceAvailabilityComponent {
  private readonly resourceService = inject(ResourceService);
  private readonly destroyRef = inject(DestroyRef);

  protected readonly loading = signal(true);
  protected readonly error = signal<string | null>(null);
  protected readonly response = signal<GetResourceAvailabilityResponse | null>(null);
  protected readonly summaries = computed<AcceleratorSummary[]>(
    () => this.response()?.summaries ?? [],
  );

  constructor() {
    this.loadAvailability();
  }

  /** Fetches physical resource availability from the backend. */
  protected loadAvailability(): void {
    this.loading.set(true);
    this.error.set(null);

    this.resourceService
      .getResourceAvailability()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.response.set(res);
          this.loading.set(false);
        },
        error: () => {
          this.error.set('Failed to load resource availability. Please try again.');
          this.loading.set(false);
        },
      });
  }
}
