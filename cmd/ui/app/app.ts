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
import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { MatIconModule } from '@angular/material/icon';
import { MatListModule } from '@angular/material/list';
import { MatSidenavModule } from '@angular/material/sidenav';
import { RouterModule, RouterOutlet } from '@angular/router';

import { Header } from './common/components/header/header';
import { AuthService } from './service/auth.service';
import { FeatureFlagService } from './service/feature_flag_service';
import { HealthService } from './service/health_service';
import { SidePanel } from './side_panel/side_panel';

/**
 * The root component.
 */
@Component({
  selector: 'app-root',
  templateUrl: './app.ng.html',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  styleUrl: './app.css',
  imports: [
    RouterOutlet,
    Header,
    SidePanel,
    MatSidenavModule,
    MatListModule,
    RouterModule,
    MatIconModule,
    CommonModule,
  ],
})
export class App {
  title = 'xmc';
  readonly isSidenavOpened = signal(true);

  private readonly authService = inject(AuthService);
  protected readonly user = toSignal(this.authService.getUser());
  private readonly healthService = inject(HealthService);
  private readonly featureFlagService = inject(FeatureFlagService);

  protected readonly isResourceDashboardEnabled =
    this.featureFlagService.isResourceDashboardEnabled;

  protected readonly isPerformanceDashboardEnabled =
    this.featureFlagService.isPerformanceDashboardEnabled;

  protected readonly isAdminPanelEnabled = toSignal(this.healthService.isAdminPanelAccessible(), {
    initialValue: false,
  });

  toggleSidenav() {
    this.isSidenavOpened.set(!this.isSidenavOpened());
  }
}
