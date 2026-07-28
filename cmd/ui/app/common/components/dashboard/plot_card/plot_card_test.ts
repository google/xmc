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

import { PlotSpec, PlotType } from 'xmanager_cloud/ui/app/service/dashboard_types';

import { XmcPlotCardComponent } from './plot_card';

const PLOT_CONTAINER_SELECTOR = '.plot-container';
const PLOT_TITLE_SELECTOR = '.plot-title';

describe('XmcPlotCardComponent', () => {
  let fixture: ComponentFixture<XmcPlotCardComponent>;
  let component: XmcPlotCardComponent;

  const mockSpec: PlotSpec = {
    plotId: 'p-1',
    title: 'Test Plot',
    type: PlotType.SCATTER,
    data: { metrics: ['loss/train'], sources: [{ xid: 1 }] },
  };

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [XmcPlotCardComponent],
    }).compileComponents();

    fixture = TestBed.createComponent(XmcPlotCardComponent);
    component = fixture.componentInstance;
    fixture.componentRef.setInput('plotSpec', mockSpec);
    fixture.detectChanges();
  });

  it('should create plot card and render title in DOM', () => {
    expect(component).toBeTruthy();
    const containerEl = fixture.nativeElement.querySelector(PLOT_CONTAINER_SELECTOR);
    expect(containerEl).toBeTruthy();
    const titleEl = fixture.nativeElement.querySelector(PLOT_TITLE_SELECTOR);
    expect(titleEl.textContent).toContain('Test Plot');
  });

  it('should switch to SUMMARY_BOX rendering when plotSpec type is SUMMARY_BOX', () => {
    fixture.componentRef.setInput('plotSpec', {
      ...mockSpec,
      type: PlotType.SUMMARY_BOX,
    });
    fixture.detectChanges();
    expect(component).toBeTruthy();
  });
});
