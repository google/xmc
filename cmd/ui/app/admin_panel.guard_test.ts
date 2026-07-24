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

import { adminPanelGuard } from './admin_panel.guard';
import { FeatureFlagService } from './service/feature_flag_service';
import { cleanState, setupModule } from './testing/test';

function setup() {
  return cleanState(() => {
    let adminPanelEnabledValue = false;
    const featureFlagServiceSpy = {
      get isAdminPanelEnabled() {
        return adminPanelEnabledValue;
      },
    };
    const routerSpy = jasmine.createSpyObj('Router', ['navigate']);

    setupModule({
      providers: [
        { provide: FeatureFlagService, useValue: featureFlagServiceSpy },
        { provide: Router, useValue: routerSpy },
      ],
    });
    return {
      setAdminPanelEnabled: (val: boolean) => {
        adminPanelEnabledValue = val;
      },
      routerSpy,
    };
  }, beforeEach);
}

describe('adminPanelGuard', () => {
  const state = setup();

  it('should return true if admin panel is enabled', () => {
    state.setAdminPanelEnabled(true);

    const result = TestBed.runInInjectionContext(() => adminPanelGuard({} as any, {} as any));

    expect(result).toBeTrue();
    expect(state.routerSpy.navigate).not.toHaveBeenCalled();
  });

  it('should redirect to / and return false if admin panel is disabled', () => {
    state.setAdminPanelEnabled(false);

    const result = TestBed.runInInjectionContext(() => adminPanelGuard({} as any, {} as any));

    expect(result).toBeFalse();
    expect(state.routerSpy.navigate).toHaveBeenCalledWith(['/']);
  });
});
