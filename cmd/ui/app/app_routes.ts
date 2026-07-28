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

import { Routes } from '@angular/router';

import { adminPanelGuard } from './admin_panel.guard';
import { AdminPanelPage } from './admin_panel/admin_panel';
import { authGuard } from './auth.guard';
import { ExperimentPage } from './experiment_page/experiment_page';
import { ExperimentsListPage } from './experiments_list_page/experiments_list_page';
import { guestGuard } from './guest.guard';
import { HomePage } from './homepage/home_page';
import { XmcDashboardsPageComponent } from './pages/dashboards/dashboards_page';
import { ClusterQueueResolver } from './resource_dashboard/resolvers/cluster_queue_resolver';
import { ResourceDashboard } from './resource_dashboard/resource_dashboard';
import { WorkUnitPage } from './work_unit_page/work_unit_page';

/**
 * The application routes.
 */
export const routes: Routes = [
  {
    path: '',
    component: HomePage,
    canActivate: [guestGuard],
    pathMatch: 'full',
  },
  {
    path: 'admin',
    component: AdminPanelPage,
    canActivate: [authGuard, adminPanelGuard],
  },
  {
    path: 'resources',
    component: ResourceDashboard,
    canActivate: [authGuard],
    resolve: { clusterQueues: ClusterQueueResolver },
  },
  {
    path: 'dashboards',
    component: XmcDashboardsPageComponent,
    canActivate: [authGuard],
  },
  {
    path: 'experiments',
    canActivate: [authGuard],
    children: [
      {
        path: '',
        component: ExperimentsListPage,
      },
      {
        path: ':xid',
        component: ExperimentPage,
      },
      {
        path: ':xid/workUnits/:wid',
        component: WorkUnitPage,
      },
    ],
  },
];
