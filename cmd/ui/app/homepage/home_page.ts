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
import { ChangeDetectionStrategy, Component, computed, inject } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { ActivatedRoute } from '@angular/router';
import { safeWindow } from 'safevalues/dom';

/**
 * Home page for the XMC UI.
 */
@Component({
  selector: 'xmc-home-page',
  standalone: true,
  templateUrl: './home_page.ng.html',
  styleUrl: './home_page.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class HomePage {
  private readonly route = inject(ActivatedRoute);

  private readonly queryParams = toSignal(this.route.queryParams);

  readonly errorMessage = computed(() => {
    const params = this.queryParams();
    if (!params) {
      return null;
    }
    const errType = params['error'];
    if (!errType) {
      return null;
    }
    const email = params['email'] || '';
    if (errType === 'forbidden') {
      return `Your account (${email}) is not authorized. Please contact your administrator to request access.`;
    }
    if (errType === 'permission_error') {
      return 'Failed to verify authorization permissions. Please contact your administrator.';
    }
    return 'An authentication error occurred. Please try again.';
  });

  login(): void {
    const returnTo = this.queryParams()?.['return_to'];
    const loginUrl = returnTo
      ? `/api/login?return_to=${encodeURIComponent(returnTo)}`
      : '/api/login';
    safeWindow.open(window, loginUrl, '_self');
  }
}
