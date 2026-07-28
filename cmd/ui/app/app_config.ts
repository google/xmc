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
import { DOCUMENT } from '@angular/common';
import { provideHttpClient } from '@angular/common/http';
import {
  APP_INITIALIZER,
  ApplicationConfig,
  inject,
  provideZoneChangeDetection,
  RendererFactory2,
} from '@angular/core';
import { provideAnimations } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';

import { routes } from './app_routes';
import { AUTH_ERROR_STYLES } from './auth-error.styles';
import { HtmlSanitizerBuilder, setElementInnerHtml } from './safevalues_helper';
import { ArtifactService } from './service/artifact_service';
import {
  AUTO_REFRESH_INTERVAL_MS,
  DEFAULT_REFRESH_INTERVAL_MS,
  USE_MOCK_DATA,
} from './service/constants';
import { DashboardService } from './service/dashboard_service';
import { ExperimentService } from './service/experiment_service';
import { StatusMessageService } from './service/status_message_service';
import { MockArtifactService } from './service/testing/mock_artifact_service';
import { MockDashboardService } from './service/testing/mock_dashboard_service';
import { MockExperimentService } from './service/testing/mock_experiment_service';
import { MockStatusMessageService } from './service/testing/mock_status_message_service';
import { MockWorkUnitsService } from './service/testing/mock_work_units_service';
import { WorkUnitsService } from './service/work_units_service';

declare global {
  interface Window {
    XMC_API_URL?: string;
    XMC_REFRESH_INTERVAL_MS?: number;
  }
}

function initializeAuthFactory(): () => Promise<void> {
  const renderer = inject(RendererFactory2).createRenderer(null, null);
  const document = inject(DOCUMENT);
  return () =>
    new Promise((resolve, reject) => {
      let baseUrl = window.XMC_API_URL;
      if (!baseUrl) {
        console.warn('XMC_API_URL is empty or not defined. Skipping auth-check.js.');
        resolve();
        return;
      }
      if (baseUrl.includes('example.com')) {
        console.warn('XMC_API_URL is using default fallback (example.com).');
      }
      baseUrl = baseUrl.replace(/\/v1\/?$/, '');

      const script = renderer.createElement('script');
      script.src = `${baseUrl}/v1/static/auth-check.js`;
      script.onload = () => resolve();
      script.onerror = () => {
        displayAuthError(document, apiErrorMessage(`${window.XMC_API_URL}`));
        renderer.removeChild(document.body, script);
        reject('Failed to load auth-check.js, cannot authenticate with IAP.');
      };
      renderer.appendChild(document.body, script);
    });
}

/**
 * The application configuration.
 */
export const appConfig: ApplicationConfig = {
  providers: [
    provideZoneChangeDetection(),
    provideHttpClient(),
    provideAnimations(),
    provideRouter(routes),
    {
      provide: APP_INITIALIZER,
      useFactory: initializeAuthFactory,
      multi: true,
    },
    {
      provide: AUTO_REFRESH_INTERVAL_MS,
      useValue: window.XMC_REFRESH_INTERVAL_MS ?? DEFAULT_REFRESH_INTERVAL_MS,
    },
    {
      provide: ExperimentService,
      useFactory: () =>
        inject(USE_MOCK_DATA) ? new MockExperimentService() : new ExperimentService(),
    },
    {
      provide: ArtifactService,
      useFactory: () => (inject(USE_MOCK_DATA) ? new MockArtifactService() : new ArtifactService()),
    },
    {
      provide: WorkUnitsService,
      useFactory: () =>
        inject(USE_MOCK_DATA) ? new MockWorkUnitsService() : new WorkUnitsService(),
    },
    {
      provide: StatusMessageService,
      useFactory: () =>
        inject(USE_MOCK_DATA) ? new MockStatusMessageService() : new StatusMessageService(),
    },
    {
      provide: DashboardService,
      useFactory: () =>
        inject(USE_MOCK_DATA) ? new MockDashboardService() : new DashboardService(),
    },
  ],
};

function apiErrorMessage(unauthorizedUri: string): string {
  return `
<div class="container">
  <img src="https://www.gstatic.com/images/icons/material/system/2x/error_outline_red_48dp.png" alt="Error">
  <h1>You don't have access</h1>
  <div>
    <h3>Troubleshooting Info</h3>
    <ul>
      <li>Check your internet connection.</li>
      <li>Make sure you are logged in with the correct account.</li>
      <li>Confirm that you have the required permissions for ${unauthorizedUri}.</li>
      <li>Contact your system administrator or support team if the problem persists.</li>
    </ul>
  </div>
</div>
`;
}

function displayAuthError(document: Document, htmlMessage: string): void {
  const errorDiv = document.createElement('div');
  errorDiv.className = 'auth-error-overlay';

  const sanitizer = new HtmlSanitizerBuilder().allowClassAttributes().build();
  setElementInnerHtml(errorDiv, sanitizer.sanitize(htmlMessage));

  const style = document.createElement('style');
  style.textContent = AUTH_ERROR_STYLES;
  document.head.appendChild(style);

  document.body.classList.add('auth-error-active');
  document.body.appendChild(errorDiv);
}
