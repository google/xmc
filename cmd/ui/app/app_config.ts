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
import {DOCUMENT} from '@angular/common';
import {APP_INITIALIZER, ApplicationConfig, inject, provideZoneChangeDetection, RendererFactory2} from '@angular/core';
import {provideRouter} from '@angular/router';
import {provideHttpClient} from '@angular/common/http';
import {provideAnimations} from '@angular/platform-browser/animations';
import {HtmlSanitizerBuilder, setElementInnerHtml} from './safevalues_helper';

import {routes} from './app_routes';
import {AUTH_ERROR_STYLES} from './auth-error.styles';
import {AUTO_REFRESH_INTERVAL_MS, DEFAULT_REFRESH_INTERVAL_MS} from './service/constants';

declare global {
  interface Window {
    XMC_API_URL?: string;
    XMC_REFRESH_INTERVAL_MS?: number;
  }
}

function initializeAuthFactory(): () => Promise<void> {
  const renderer = inject(RendererFactory2).createRenderer(null, null);
  const document = inject(DOCUMENT);
  return () => new Promise((resolve, reject) => {
           if (window.XMC_API_URL) {
             let baseUrl = window.XMC_API_URL;
             if (baseUrl.endsWith('/v1')) {
               baseUrl = baseUrl.substring(0, baseUrl.length - 3);
             }
             if (baseUrl.endsWith('/v1/')) {
               baseUrl = baseUrl.substring(0, baseUrl.length - 4);
             }
             const script = renderer.createElement('script');
             script.src = `${baseUrl}/v1/static/auth-check.js`;
             script.onload = () => {
               resolve();
             };
             script.onerror = () => {
               // Display error message to the user.
               const errorHtml = apiErrorMessage(`${window.XMC_API_URL}`);
               displayAuthError(document, errorHtml);

               // We reject here to stop app initialization on error,
               // but still want to remove script to avoid clutter.
               renderer.removeChild(document.body, script);
               reject(
                   'Failed to load auth-check.js, cannot authenticate with IAP.');
             };
             renderer.appendChild(document.body, script);
           } else {
             console.warn('XMC_API_URL is empty or not defined. Skipping auth-check.js.');
             // If no API URL is set, we can't authenticate, but we
             // resolve so app can load in a non-functional state.
             resolve();
           }
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
  ]
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
      <li>Confirm that you have the required permissions for ${
      unauthorizedUri}.</li>
      <li>Contact your system administrator or support team if the problem persists.</li>
    </ul>
  </div>
</div>
`;
}

function displayAuthError(document: Document, htmlMessage: string): void {
  const errorDiv = document.createElement('div');
  errorDiv.className = 'auth-error-overlay';

  const sanitizer = new HtmlSanitizerBuilder()
                        .allowClassAttributes()
                        .build();
  setElementInnerHtml(errorDiv, sanitizer.sanitize(htmlMessage));

  const style = document.createElement('style');
  style.textContent = AUTH_ERROR_STYLES;
  document.head.appendChild(style);

  document.body.classList.add('auth-error-active');
  document.body.appendChild(errorDiv);
}