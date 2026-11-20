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

import { ChangeDetectionStrategy, Component, inject, OnInit } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { ResourceService } from 'app/service/resource_service';

/**
 * Minimal Component to wire up the Resource Availability API.
 * Full UI will be implemented in PR 2.
 */
@Component({
  selector: 'xmc-rod-resource-availability',
  standalone: true,
  template: '<div>Physical Resource Availability Data Loaded: {{ summariesCount() }} types.</div>',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class ResourceAvailabilityComponent implements OnInit {
  private readonly resourceService = inject(ResourceService);

  protected readonly response = toSignal(this.resourceService.getResourceAvailability());

  protected summariesCount() {
    return this.response()?.summaries?.length ?? 0;
  }

  ngOnInit() {
    // Force API call
  }
}
