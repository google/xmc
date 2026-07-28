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

import { JsonPipe } from '@angular/common';
import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { HealthService } from '../service/health_service';

/**
 * Mock Admin Panel component for XMC.
 */
@Component({
  selector: 'xmc-admin-panel',
  standalone: true,
  imports: [JsonPipe],
  templateUrl: './admin_panel.ng.html',
  styleUrl: './admin_panel.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class AdminPanelPage {
  private readonly healthService = inject(HealthService);

  protected readonly healthStatus = toSignal(
    this.healthService.getHealthStatus().pipe(
      catchError((error) => {
        console.error('Failed to fetch health status', error);
        return of({ error: 'Failed to fetch health status' });
      }),
    ),
  );

  protected readonly healthConfigs = toSignal(
    this.healthService.getHealthConfigs().pipe(
      catchError((error) => {
        console.error('Failed to fetch health configs', error);
        return of({ error: 'Failed to fetch health configs' });
      }),
    ),
  );
}
