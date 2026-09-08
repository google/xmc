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
import 'jasmine';

import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { of } from 'rxjs';

import { App } from './app';
import { AuthService } from './service/auth.service';
import { FeatureFlagService } from './service/feature_flag_service';
import { HealthService } from './service/health_service';
import { bootstrap, hasEl, setupModule } from './testing/test';

describe('App Component', () => {
  let isResourceDashboardEnabled = true;
  let isPerformanceDashboardEnabled = true;
  let isNonProdAuthFlag = false;
  let userIsNonProdAuth = false;

  beforeEach(() => {
    isResourceDashboardEnabled = true;
    isPerformanceDashboardEnabled = true;
    isNonProdAuthFlag = false;
    userIsNonProdAuth = false;

    const authServiceMock = jasmine.createSpyObj<AuthService>('AuthService', ['getUser']);
    authServiceMock.getUser.and.callFake(() =>
      of({
        email: 'user@google.com',
        name: 'Test User',
        pictureUrl: 'pic.jpg',
        sessionExpiresAt: 12345,
        allowedDomains: ['google.com'],
        isNonProdAuth: userIsNonProdAuth,
      }),
    );

    const healthServiceMock = jasmine.createSpyObj<HealthService>('HealthService', [
      'isAdminPanelAccessible',
    ]);
    healthServiceMock.isAdminPanelAccessible.and.returnValue(of(false));

    const featureFlagServiceMock = {
      get isResourceDashboardEnabled() {
        return isResourceDashboardEnabled;
      },
      get isPerformanceDashboardEnabled() {
        return isPerformanceDashboardEnabled;
      },
      get isAdminPanelEnabled() {
        return false;
      },
      get isNonProdAuth() {
        return isNonProdAuthFlag;
      },
    } as FeatureFlagService;

    setupModule({
      providers: [
        provideNoopAnimations(),
        provideRouter([]),
        { provide: AuthService, useValue: authServiceMock },
        { provide: HealthService, useValue: healthServiceMock },
        { provide: FeatureFlagService, useValue: featureFlagServiceMock },
      ],
      imports: [App],
    });
  });

  it('should create the app', () => {
    const comp = bootstrap(App);
    expect(comp).toBeTruthy();
  });

  it('should display Resources menu item when isResourceDashboardEnabled is true', () => {
    bootstrap(App);
    expect(hasEl('a[routerLink="/resources"]')).toBeTrue();
  });

  it('should hide Resources menu item when isResourceDashboardEnabled is false', () => {
    isResourceDashboardEnabled = false;
    bootstrap(App);
    expect(hasEl('a[routerLink="/resources"]')).toBeFalse();
  });

  it('should display Dashboards menu item when isPerformanceDashboardEnabled is true', () => {
    bootstrap(App);
    expect(hasEl('a[routerLink="/dashboards"]')).toBeTrue();
  });

  it('should hide Dashboards menu item when isPerformanceDashboardEnabled is false', () => {
    isPerformanceDashboardEnabled = false;
    bootstrap(App);
    expect(hasEl('a[routerLink="/dashboards"]')).toBeFalse();
  });

  it('should display non-prod auth banner when isNonProdAuth feature flag is true', () => {
    isNonProdAuthFlag = true;
    bootstrap(App);
    expect(hasEl('.non-prod-auth-banner')).toBeTrue();
  });

  it('should display non-prod auth banner when user isNonProdAuth is true', () => {
    userIsNonProdAuth = true;
    bootstrap(App);
    expect(hasEl('.non-prod-auth-banner')).toBeTrue();
  });

  it('should hide non-prod auth banner when non-prod auth is false', () => {
    isNonProdAuthFlag = false;
    userIsNonProdAuth = false;
    bootstrap(App);
    expect(hasEl('.non-prod-auth-banner')).toBeFalse();
  });
});
