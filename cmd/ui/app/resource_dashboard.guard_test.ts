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
import { Router } from '@angular/router';

import { resourceDashboardGuard } from './resource_dashboard.guard';
import { FeatureFlagService } from './service/feature_flag_service';
import { cleanState, setupModule } from './testing/test';

function setup() {
  return cleanState(() => {
    let resourceDashboardEnabledValue = false;

    const featureFlagServiceSpy = {
      get isResourceDashboardEnabled() {
        return resourceDashboardEnabledValue;
      },
    };
    const routerSpy = jasmine.createSpyObj('Router', ['parseUrl']);
    routerSpy.parseUrl.and.callFake((url: string) => url as any);

    setupModule({
      providers: [
        { provide: FeatureFlagService, useValue: featureFlagServiceSpy },
        { provide: Router, useValue: routerSpy },
      ],
    });
    return {
      setResourceDashboardEnabled: (val: boolean) => {
        resourceDashboardEnabledValue = val;
      },
      routerSpy,
    };
  }, beforeEach);
}

describe('resourceDashboardGuard', () => {
  const state = setup();

  it('should return true if resource dashboard is enabled', () => {
    state.setResourceDashboardEnabled(true);

    const result = TestBed.runInInjectionContext(() =>
      resourceDashboardGuard({} as any, {} as any),
    );

    expect(result).toBeTrue();
    expect(state.routerSpy.parseUrl).not.toHaveBeenCalled();
  });

  it('should return UrlTree redirecting to / if resource dashboard is disabled', () => {
    state.setResourceDashboardEnabled(false);

    const result = TestBed.runInInjectionContext(() =>
      resourceDashboardGuard({} as any, {} as any),
    );

    expect(result).toEqual('/' as any);
    expect(state.routerSpy.parseUrl).toHaveBeenCalledWith('/');
  });
});
