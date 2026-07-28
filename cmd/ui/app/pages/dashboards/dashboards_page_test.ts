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
import { MatDialog } from '@angular/material/dialog';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { ActivatedRoute } from '@angular/router';
import { of } from 'rxjs';

import { AutoReloadService } from 'xmanager_cloud/ui/app/common/components/autoreloader/autoreloader_service';
import { MOCK_DASHBOARDS_LIST } from 'xmanager_cloud/ui/app/common/mock_data';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';

import { XmcDashboardsPageComponent } from './dashboards_page';

const ADD_DASHBOARD_BTN_SELECTOR = '.add-dashboard-btn';
const AUTO_REFRESH_BTN_SELECTOR = '.auto-refresh-btn';

describe('XmcDashboardsPageComponent', () => {
  let fixture: ComponentFixture<XmcDashboardsPageComponent>;
  let component: XmcDashboardsPageComponent;
  let mockDashboardService: jasmine.SpyObj<DashboardService>;
  let mockAutoReloadService: jasmine.SpyObj<AutoReloadService>;
  let mockDialog: jasmine.SpyObj<MatDialog>;
  let mockActivatedRoute: any;

  beforeEach(async () => {
    mockActivatedRoute = {
      snapshot: { queryParamMap: { get: jasmine.createSpy('get').and.returnValue(null) } },
    };

    mockDashboardService = jasmine.createSpyObj('DashboardService', [
      'listDashboards',
      'createDashboard',
      'getChartData',
    ]);
    mockDashboardService.listDashboards.and.returnValue(of({ dashboards: MOCK_DASHBOARDS_LIST }));
    mockDashboardService.getChartData.and.returnValue(
      of({ name: 'experiments/1/charts/c-1', xid: 1, plotsData: [] }),
    );

    mockAutoReloadService = jasmine.createSpyObj('AutoReloadService', ['createReloader']);
    mockAutoReloadService.createReloader.and.returnValue({
      reloader$: of(undefined) as any,
      start: jasmine.createSpy('start'),
      stop: jasmine.createSpy('stop'),
    });

    mockDialog = jasmine.createSpyObj('MatDialog', ['open']);
    mockDialog.open.and.returnValue({
      afterClosed: () => of(undefined),
    } as any);

    await TestBed.configureTestingModule({
      imports: [XmcDashboardsPageComponent, NoopAnimationsModule],
      providers: [
        { provide: DashboardService, useValue: mockDashboardService },
        { provide: AutoReloadService, useValue: mockAutoReloadService },
        { provide: MatDialog, useValue: mockDialog },
        { provide: ActivatedRoute, useValue: mockActivatedRoute },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(XmcDashboardsPageComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create dashboards page and render Add performance dashboard button', () => {
    expect(component).toBeTruthy();
    const addBtn = fixture.nativeElement.querySelector(ADD_DASHBOARD_BTN_SELECTOR);
    expect(addBtn).toBeTruthy();
    expect(addBtn.textContent).toContain('Add performance dashboard');
  });

  it('should open Add performance dashboard modal popup when button is clicked', () => {
    const addBtn = fixture.nativeElement.querySelector(ADD_DASHBOARD_BTN_SELECTOR);
    addBtn.click();
    expect(mockDialog.open).toHaveBeenCalled();
  });

  it('should toggle auto refresh state when auto-refresh button is clicked', () => {
    expect(component.autoRefreshEnabled()).toBeTrue();
    const autoRefreshBtn = fixture.nativeElement.querySelector(AUTO_REFRESH_BTN_SELECTOR);
    autoRefreshBtn.click();
    expect(component.autoRefreshEnabled()).toBeFalse();
  });
});
