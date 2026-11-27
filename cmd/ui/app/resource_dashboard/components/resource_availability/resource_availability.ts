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
  OnInit,
  signal,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { Subscription } from 'rxjs';

import { ErrorBannerComponent } from 'app/common/components/error_banner/error_banner';
import { AcceleratorSummary } from 'app/protos/rod';
import { ResourceService } from 'app/service/resource_service';

/** Aggregated physical GPU metrics for a single accelerator type. */
export interface AcceleratorCardSummary {
  acceleratorType: string;
  allocated: number;
  unassigned: number;
  active: number;
  total: number;
  reserved: number;
  activeNodes: number;
  totalNodes: number;
}

function createEmptyCard(acceleratorType: string): AcceleratorCardSummary {
  return {
    acceleratorType,
    allocated: 0,
    unassigned: 0,
    active: 0,
    total: 0,
    reserved: 0,
    activeNodes: 0,
    totalNodes: 0,
  };
}

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
export class ResourceAvailabilityComponent implements OnInit {
  private readonly resourceService = inject(ResourceService);
  private readonly destroyRef = inject(DestroyRef);

  protected readonly loading = signal(true);
  protected readonly error = signal<string | null>(null);
  protected readonly summaries = signal<AcceleratorSummary[]>([]);

  protected readonly acceleratorCards = computed<AcceleratorCardSummary[]>(() => {
    const byType = new Map<string, AcceleratorCardSummary>();
    for (const item of this.summaries()) {
      const accelType = item.acceleratorType || 'unknown';
      const card = byType.get(accelType) ?? createEmptyCard(accelType);
      byType.set(accelType, card);
      this.addSummaryToCard(card, item);
    }
    return Array.from(byType.values()).sort((a, b) =>
      a.acceleratorType.localeCompare(b.acceleratorType),
    );
  });

  private availabilitySubscription?: Subscription;

  ngOnInit(): void {
    this.loadAvailability();
  }

  /** Fetches physical resource availability from the backend. */
  protected loadAvailability(): void {
    this.availabilitySubscription?.unsubscribe();
    this.loading.set(true);
    this.error.set(null);

    this.availabilitySubscription = this.resourceService
      .getResourceAvailability()
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (res) => {
          this.summaries.set(res.summaries ?? []);
          this.loading.set(false);
        },
        error: () => {
          this.error.set('Failed to load resource availability. Please try again.');
          this.loading.set(false);
        },
      });
  }

  /** Returns the number of ready but unallocated accelerators in a summary row. */
  protected unassignedCount(item: AcceleratorSummary): number {
    return Math.max(0, (item.activeAccelerators ?? 0) - (item.allocatedAccelerators ?? 0));
  }

  private addSummaryToCard(card: AcceleratorCardSummary, item: AcceleratorSummary): void {
    const total = item.totalAccelerators ?? 0;
    card.allocated += item.allocatedAccelerators ?? 0;
    card.unassigned += this.unassignedCount(item);
    card.active += item.activeAccelerators ?? 0;
    card.total += total;
    if (item.reservation) {
      card.reserved += total;
    }
    card.activeNodes += item.activeNodes ?? 0;
    card.totalNodes += item.totalNodes ?? 0;
  }
}
