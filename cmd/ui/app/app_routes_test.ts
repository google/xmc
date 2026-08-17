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

import { adminPanelGuard } from './admin_panel.guard';
import { AdminPanelPage } from './admin_panel/admin_panel';
import { routes } from './app_routes';
import { authGuard } from './auth.guard';
import { ExperimentPage } from './experiment_page/experiment_page';
import { ExperimentsListPage } from './experiments_list_page/experiments_list_page';
import { guestGuard } from './guest.guard';
import { HomePage } from './homepage/home_page';
import { DashboardsListPage } from './pages/dashboards/dashboards_list_page/dashboards_list_page';
import { DashboardsPage } from './pages/dashboards/dashboards_page';
import { performanceDashboardGuard } from './performance_dashboard.guard';
import { resourceDashboardGuard } from './resource_dashboard.guard';
import { ResourceDashboard } from './resource_dashboard/resource_dashboard';
import { WorkUnitPage } from './work_unit_page/work_unit_page';

describe('App Routes', () => {
  it('should define route configuration', () => {
    expect(routes).toBeDefined();
    expect(routes.length).toBe(5);
  });

  it('should route "" to HomePage with guestGuard', () => {
    const route = routes.find((r) => r.path === '');
    expect(route).toBeDefined();
    expect(route?.component).toBe(HomePage);
    expect(route?.canActivate).toContain(guestGuard);
    expect(route?.pathMatch).toBe('full');
  });

  it('should route "admin" to AdminPanelPage with authGuard and adminPanelGuard', () => {
    const route = routes.find((r) => r.path === 'admin');
    expect(route).toBeDefined();
    expect(route?.component).toBe(AdminPanelPage);
    expect(route?.canActivate).toEqual([authGuard, adminPanelGuard]);
  });

  it('should route "resources" to ResourceDashboard with authGuard and resourceDashboardGuard', () => {
    const route = routes.find((r) => r.path === 'resources');
    expect(route).toBeDefined();
    expect(route?.component).toBe(ResourceDashboard);
    expect(route?.canActivate).toEqual([authGuard, resourceDashboardGuard]);
  });

  it('should route "dashboards" with authGuard and performanceDashboardGuard and children', () => {
    const route = routes.find((r) => r.path === 'dashboards');
    expect(route).toBeDefined();
    expect(route?.canActivate).toEqual([authGuard, performanceDashboardGuard]);
    expect(route?.children?.length).toBe(2);

    const listChild = route?.children?.find((r) => r.path === '');
    expect(listChild).toBeDefined();
    expect(listChild?.component).toBe(DashboardsListPage);

    const detailChild = route?.children?.find((r) => r.path === ':id');
    expect(detailChild).toBeDefined();
    expect(detailChild?.component).toBe(DashboardsPage);
  });

  it('should route "experiments" with authGuard and children', () => {
    const parentRoute = routes.find((r) => r.path === 'experiments');
    expect(parentRoute).toBeDefined();
    expect(parentRoute?.canActivate).toContain(authGuard);
    expect(parentRoute?.children).toBeDefined();
    expect(parentRoute?.children?.length).toBe(3);

    const listChild = parentRoute?.children?.find((r) => r.path === '');
    expect(listChild).toBeDefined();
    expect(listChild?.component).toBe(ExperimentsListPage);

    const detailChild = parentRoute?.children?.find((r) => r.path === ':xid');
    expect(detailChild).toBeDefined();
    expect(detailChild?.component).toBe(ExperimentPage);

    const wuChild = parentRoute?.children?.find((r) => r.path === ':xid/workUnits/:wid');
    expect(wuChild).toBeDefined();
    expect(wuChild?.component).toBe(WorkUnitPage);
  });
});
