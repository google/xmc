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

import 'jasmine';

import { TestBed } from '@angular/core/testing';
import { Router, UrlTree } from '@angular/router';

import { resourceDashboardGuard } from './resource_dashboard.guard';
import { FeatureFlagService } from './service/feature_flag_service';
import { cleanState, setupModule } from './testing/test';

function setup(isResourceDashboardEnabled: boolean) {
  return cleanState(() => {
    const featureFlagServiceSpy = jasmine.createSpyObj<FeatureFlagService>(
      'FeatureFlagService',
      [],
      { isResourceDashboardEnabled },
    );
    const routerSpy = jasmine.createSpyObj<Router>('Router', ['parseUrl']);
    routerSpy.parseUrl.and.callFake((url: string) => url as unknown as UrlTree);

    setupModule({
      providers: [
        { provide: FeatureFlagService, useValue: featureFlagServiceSpy },
        { provide: Router, useValue: routerSpy },
      ],
    });
    return {
      featureFlagServiceSpy,
      routerSpy,
    };
  }, beforeEach);
}

describe('resourceDashboardGuard', () => {
  describe('when resource dashboard is enabled', () => {
    const state = setup(true);

    it('should return true and not redirect', () => {
      const result = TestBed.runInInjectionContext(() =>
        resourceDashboardGuard({} as any, {} as any),
      );

      expect(result).toBeTrue();
      expect(state.routerSpy.parseUrl).not.toHaveBeenCalled();
    });
  });

  describe('when resource dashboard is disabled', () => {
    const state = setup(false);

    it('should return UrlTree redirecting to /', () => {
      const result = TestBed.runInInjectionContext(() =>
        resourceDashboardGuard({} as any, {} as any),
      );

      expect(result).toEqual('/' as unknown as UrlTree);
      expect(state.routerSpy.parseUrl).toHaveBeenCalledWith('/');
    });
  });
});
