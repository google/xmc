// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import 'jasmine';

import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialogRef } from '@angular/material/dialog';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { Dashboard, LayoutType, PlotType } from 'xmanager_cloud/ui/app/service/dashboard_models';

import { CreateDashboardDialogComponent, getDefaultPlotSpecs } from './create_dashboard_dialog';

const ADD_COMPARISON_BUTTON_SELECTOR = '.add-comp-btn';

describe('CreateDashboardDialogComponent', () => {
  let fixture: ComponentFixture<CreateDashboardDialogComponent>;
  let component: CreateDashboardDialogComponent;
  let dialogRefSpy: jasmine.SpyObj<MatDialogRef<CreateDashboardDialogComponent>>;

  beforeEach(async () => {
    dialogRefSpy = jasmine.createSpyObj('MatDialogRef', ['close']);

    await TestBed.configureTestingModule({
      imports: [CreateDashboardDialogComponent, NoopAnimationsModule],
      providers: [{ provide: MatDialogRef, useValue: dialogRefSpy }],
    }).compileComponents();

    fixture = TestBed.createComponent(CreateDashboardDialogComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should initialize with default plot specs and invalid form', () => {
    expect(component.form.invalid).toBeTrue();
    expect(component.plots().length).toBe(getDefaultPlotSpecs().length);
  });

  it('should render correct chip classes and labels for SUMMARY_BOX and SCATTER plots', () => {
    const chips = fixture.nativeElement.querySelectorAll('.plot-type-chip');
    expect(chips.length).toBe(4);

    const chipTexts = Array.from(chips).map((el: any) => el.textContent.trim());
    expect(chipTexts.filter((t) => t === 'Summary KPI').length).toBe(2);
    expect(chipTexts.filter((t) => t === 'Curve Plot').length).toBe(2);

    const kpiChips = fixture.nativeElement.querySelectorAll('.plot-type-chip.kpi-chip');
    const curveChips = fixture.nativeElement.querySelectorAll('.plot-type-chip.curve-chip');
    expect(kpiChips.length).toBe(2);
    expect(curveChips.length).toBe(2);
  });

  it('should add custom plot when valid metric name is entered', () => {
    const initialCount = component.plots().length;
    component.newPlotForm.patchValue({
      type: PlotType.SUMMARY_BOX,
      metricName: 'custom/f1_score',
      title: 'Last F1 Score',
    });

    component.addPlot();

    expect(component.plots().length).toBe(initialCount + 1);
    const added = component.plots()[component.plots().length - 1];
    expect(added.title).toBe('Last F1 Score');
    expect(added.data?.metrics).toEqual(['custom/f1_score']);
    expect(added.plotId).toMatch(/^custom_f1_score_[a-z0-9]+$/);
  });

  it('should generate collision-resistant plotIds when adding multiple plots rapidly', () => {
    component.newPlotForm.patchValue({
      type: PlotType.SUMMARY_BOX,
      metricName: 'val/loss',
      title: 'Validation Loss 1',
    });
    component.addPlot();

    component.newPlotForm.patchValue({
      type: PlotType.SUMMARY_BOX,
      metricName: 'val/loss',
      title: 'Validation Loss 2',
    });
    component.addPlot();

    const plots = component.plots();
    const lastPlot = plots[plots.length - 1];
    const secondLastPlot = plots[plots.length - 2];

    expect(lastPlot.plotId).not.toBe(secondLastPlot.plotId);
    expect(lastPlot.plotId).toMatch(/^val_loss_[a-z0-9]+$/);
    expect(secondLastPlot.plotId).toMatch(/^val_loss_[a-z0-9]+$/);
  });

  it('should remove a plot by index', () => {
    const initialCount = component.plots().length;
    component.removePlot(0);
    expect(component.plots().length).toBe(initialCount - 1);
  });

  it('should reset plots to default', () => {
    component.removePlot(0);
    component.removePlot(0);
    expect(component.plots().length).toBe(2);

    component.resetPlotsToDefault();
    expect(component.plots().length).toBe(4);
  });

  it('should add and remove comparison experiments', () => {
    component.newComparisonForm.patchValue({
      xid: 234911123,
      wid: 1,
      title: 'VCC tuned_v1',
    });
    component.addComparison();

    expect(component.comparisons().length).toBe(1);
    expect(component.comparisons()[0].xid).toBe(234911123);

    component.removeComparison(0);
    expect(component.comparisons().length).toBe(0);
  });

  it('should render "Add Experiment" on the comparison button and add comparison when clicked', () => {
    const addButton = fixture.nativeElement.querySelector(
      ADD_COMPARISON_BUTTON_SELECTOR,
    ) as HTMLButtonElement;
    expect(addButton).toBeTruthy();
    expect(addButton.textContent?.trim()).toContain('Add Experiment');

    component.newComparisonForm.patchValue({
      xid: 234911123,
      wid: 1,
      title: 'VCC tuned_v1',
    });
    fixture.detectChanges();

    expect(addButton.disabled).toBeFalse();
    addButton.click();
    fixture.detectChanges();

    expect(component.comparisons().length).toBe(1);
    expect(component.comparisons()[0].xid).toBe(234911123);
  });

  it('should not add comparison experiment when form is invalid or WID is null', () => {
    component.newComparisonForm.patchValue({
      xid: -1,
      wid: 1,
    });
    component.addComparison();
    expect(component.comparisons().length).toBe(0);

    component.newComparisonForm.patchValue({
      xid: 234911123,
      wid: null,
    });
    expect(component.newComparisonForm.invalid).toBeTrue();
    component.addComparison();
    expect(component.comparisons().length).toBe(0);
  });

  it('should be invalid when baseWid is null', () => {
    component.form.patchValue({
      title: 'Benchmark',
      baseXid: 234911122,
      baseWid: null,
    });
    expect(component.form.invalid).toBeTrue();
  });

  it('should build and return Dashboard object on submit', () => {
    component.form.patchValue({
      title: 'Custom Evaluation Benchmark',
      description: 'Benchmark dashboard comparing tuning iterations',
      baseXid: 234911122,
      baseWid: 1,
      baseChartTitle: 'VCC Baseline',
    });

    component.newComparisonForm.patchValue({
      xid: 234911123,
      wid: 1,
      title: 'VCC tuned_v1',
    });
    component.addComparison();

    expect(component.form.valid).toBeTrue();
    component.onSubmit();

    expect(dialogRefSpy.close).toHaveBeenCalledTimes(1);
    const resultDashboard = dialogRefSpy.close.calls.mostRecent().args[0] as Dashboard;

    expect(resultDashboard.title).toBe('Custom Evaluation Benchmark');
    expect(resultDashboard.description).toBe('Benchmark dashboard comparing tuning iterations');
    expect(resultDashboard.chartLayout).toEqual({ type: LayoutType.SIDE_BY_SIDE });
    expect(resultDashboard.charts?.length).toBe(2);

    const baseChart = resultDashboard.charts![0];
    expect(baseChart.xid).toBe(234911122);
    expect(baseChart.wid).toBe(1);
    expect(baseChart.title).toBe('VCC Baseline');
    expect(baseChart.plots?.length).toBe(4);

    const compChart = resultDashboard.charts![1];
    expect(compChart.xid).toBe(234911123);
    expect(compChart.title).toBe('VCC tuned_v1');
  });

  it('should close without value on cancel', () => {
    component.onCancel();
    expect(dialogRefSpy.close).toHaveBeenCalledWith();
  });
});
