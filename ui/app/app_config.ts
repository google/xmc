import {DOCUMENT} from '@angular/common';
import {APP_INITIALIZER, ApplicationConfig, inject, provideZonelessChangeDetection, RendererFactory2} from '@angular/core';
import {provideRouter} from '@angular/router';
import {HtmlSanitizerBuilder} from 'safevalues';
import {setElementInnerHtml} from 'safevalues/dom';

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
             const script = renderer.createElement('script');
             script.src = `${window.XMC_API_URL}/static/auth-check.js`;
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
             console.error('XMC_API_URL is not defined on window.');
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
    provideZonelessChangeDetection(),
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