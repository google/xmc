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

import { routes } from './app_routes';
import { HomePage } from './homepage/home_page';
import { ExperimentsListPage } from './experiments_list_page/experiments_list_page';
import { ExperimentPage } from './experiment_page/experiment_page';
import { WorkUnitPage } from './work_unit_page/work_unit_page';
import { authGuard } from './auth.guard';

describe('App Routes', () => {
  it('should define route configuration', () => {
    expect(routes).toBeDefined();
    expect(routes.length).toBe(4);
  });

  it('should route "" to HomePage', () => {
    const route = routes.find(r => r.path === '');
    expect(route).toBeDefined();
    expect(route?.component).toBe(HomePage);
    expect(route?.pathMatch).toBe('full');
  });

  it('should route "experiments" to ExperimentsListPage with authGuard', () => {
    const route = routes.find(r => r.path === 'experiments');
    expect(route).toBeDefined();
    expect(route?.component).toBe(ExperimentsListPage);
    expect(route?.canActivate).toContain(authGuard);
  });

  it('should route "experiments/:xid" to ExperimentPage with authGuard', () => {
    const route = routes.find(r => r.path === 'experiments/:xid');
    expect(route).toBeDefined();
    expect(route?.component).toBe(ExperimentPage);
    expect(route?.canActivate).toContain(authGuard);
  });

  it('should route "experiments/:xid/workUnits/:wid" to WorkUnitPage with authGuard', () => {
    const route = routes.find(r => r.path === 'experiments/:xid/workUnits/:wid');
    expect(route).toBeDefined();
    expect(route?.component).toBe(WorkUnitPage);
    expect(route?.canActivate).toContain(authGuard);
  });
});
