// Copyright 2025 Google LLC
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
import {Component, ChangeDetectionStrategy, inject, computed, effect} from '@angular/core';
import {Router, ActivatedRoute} from '@angular/router';
import {AuthService} from '../service/auth.service';
import {toSignal} from '@angular/core/rxjs-interop';

/**
 * Home page for the XMC UI.
 */
@Component({
  selector: 'xmc-home-page',
  standalone: true,
  templateUrl: './home_page.ng.html',
  styleUrl: './home_page.css',
  changeDetection: ChangeDetectionStrategy.OnPush
})
export class HomePage {
  private readonly authService = inject(AuthService);
  private readonly router = inject(Router);
  private readonly route = inject(ActivatedRoute);

  private readonly queryParams = toSignal(this.route.queryParams);
  private readonly user = toSignal(this.authService.getUser());

  readonly errorMessage = computed(() => {
    const params = this.queryParams();
    if (!params) {
      return null;
    }
    const errType = params['error'];
    if (!errType) {
      return null;
    }
    if (errType === 'forbidden') {
      const email = params['email'] ?? '';
      return `Your account (${email}) is not authorized. Please contact your administrator to request access.`;
    } else if (errType === 'permission_error') {
      return 'Failed to verify authorization permissions. Please contact your administrator.';
    } else {
      return 'An authentication error occurred. Please try again.';
    }
  });

  constructor() {
    effect(() => {
      const user = this.user();
      if (user && user.email) {
        this.router.navigate(['/experiments']);
      }
    });
  }

  login() {
    window.open('/api/login', '_self');
  }
}
