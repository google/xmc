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
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { of } from 'rxjs';

import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';
import { Dashboard, LayoutType } from 'xmanager_cloud/ui/app/service/dashboard_types';

import { XmcDashboardGridComponent } from './dashboard_grid';

const GRID_CONTAINER_SELECTOR = '.grid-container';
const GRID_CELL_SELECTOR = '.grid-cell';

describe('XmcDashboardGridComponent', () => {
  let fixture: ComponentFixture<XmcDashboardGridComponent>;
  let component: XmcDashboardGridComponent;
  let mockDashboardService: jasmine.SpyObj<DashboardService>;

  const mockDashboard: Dashboard = {
    name: 'dashboards/dash-1',
    title: 'Test Dashboard',
    charts: [
      {
        name: 'experiments/1/charts/c-1',
        title: 'Chart 1',
        xid: 1,
        plots: [],
        plotLayout: { type: LayoutType.STACKED },
      },
    ],
    chartLayout: { type: LayoutType.SIDE_BY_SIDE },
  };

  beforeEach(async () => {
    mockDashboardService = jasmine.createSpyObj('DashboardService', ['getChartData']);
    mockDashboardService.getChartData.and.returnValue(
      of({ name: 'experiments/1/charts/c-1', xid: 1, plotsData: [] }),
    );

    await TestBed.configureTestingModule({
      imports: [XmcDashboardGridComponent],
      providers: [{ provide: DashboardService, useValue: mockDashboardService }],
    }).compileComponents();

    fixture = TestBed.createComponent(XmcDashboardGridComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('dashboard', mockDashboard);
    fixture.detectChanges();
  });

  it('should create grid container and render chart cells in DOM', () => {
    expect(component).toBeTruthy();
    const container = fixture.nativeElement.querySelector(GRID_CONTAINER_SELECTOR);
    expect(container).toBeTruthy();
    const cells = fixture.nativeElement.querySelectorAll(GRID_CELL_SELECTOR);
    expect(cells.length).toBe(1);
  });
});
