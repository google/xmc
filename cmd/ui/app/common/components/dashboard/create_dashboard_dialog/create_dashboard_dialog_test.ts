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

import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialogRef } from '@angular/material/dialog';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';

import { LayoutType, PlotType } from 'xmanager_cloud/ui/app/service/dashboard_models';

import {
  CreateDashboardDialogComponent,
  CreateDashboardDialogResult,
  getDefaultPlotSpecs,
} from './create_dashboard_dialog';
import { CreateDashboardDialogHarness } from './create_dashboard_dialog_harness';

describe('CreateDashboardDialogComponent', () => {
  let fixture: ComponentFixture<CreateDashboardDialogComponent>;
  let component: CreateDashboardDialogComponent;
  let harness: CreateDashboardDialogHarness;
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
    harness = await TestbedHarnessEnvironment.harnessForFixture(
      fixture,
      CreateDashboardDialogHarness,
    );
  });

  it('should initialize with default plot specs and invalid form', async () => {
    expect(component.form.invalid).toBeTrue();
    expect(await harness.getPlotCount()).toBe(getDefaultPlotSpecs().length);
    expect(component.plots().length).toBe(getDefaultPlotSpecs().length);
  });

  it('should render correct chip classes and labels for SUMMARY_BOX and SCATTER plots', async () => {
    const plotItems = await harness.getPlotItems();
    expect(plotItems.length).toBe(4);

    const chipTexts = await Promise.all(plotItems.map((p) => p.getChipText()));
    expect(chipTexts.filter((t) => t === 'Summary KPI').length).toBe(2);
    expect(chipTexts.filter((t) => t === 'Curve Plot').length).toBe(2);

    const kpiCount = (await Promise.all(plotItems.map((p) => p.isKpiChip()))).filter(
      Boolean,
    ).length;
    const curveCount = (await Promise.all(plotItems.map((p) => p.isCurveChip()))).filter(
      Boolean,
    ).length;
    expect(kpiCount).toBe(2);
    expect(curveCount).toBe(2);
  });

  it('should add custom plot when valid metric name is entered', async () => {
    const initialCount = await harness.getPlotCount();
    component.newPlotForm.patchValue({
      type: PlotType.SUMMARY_BOX,
      metricName: 'custom/f1_score',
      title: 'Last F1 Score',
    });

    await harness.clickAddPlot();

    expect(await harness.getPlotCount()).toBe(initialCount + 1);
    const added = component.plots()[component.plots().length - 1];
    expect(added.title).toBe('Last F1 Score');
    expect(added.data?.metrics).toEqual(['custom/f1_score']);
    expect(added.plotId).toMatch(/^custom_f1_score_[a-z0-9]+$/);
  });

  it('should generate collision-resistant plotIds when adding multiple plots rapidly', async () => {
    component.newPlotForm.patchValue({
      type: PlotType.SUMMARY_BOX,
      metricName: 'val/loss',
      title: 'Validation Loss 1',
    });
    await harness.clickAddPlot();

    component.newPlotForm.patchValue({
      type: PlotType.SUMMARY_BOX,
      metricName: 'val/loss',
      title: 'Validation Loss 2',
    });
    await harness.clickAddPlot();

    const plots = component.plots();
    const lastPlot = plots[plots.length - 1];
    const secondLastPlot = plots[plots.length - 2];

    expect(lastPlot.plotId).not.toBe(secondLastPlot.plotId);
    expect(lastPlot.plotId).toMatch(/^val_loss_[a-z0-9]+$/);
    expect(secondLastPlot.plotId).toMatch(/^val_loss_[a-z0-9]+$/);
  });

  it('should remove a plot by index', async () => {
    const initialCount = await harness.getPlotCount();
    await harness.removePlot(0);
    expect(await harness.getPlotCount()).toBe(initialCount - 1);
  });

  it('should reset plots to default', async () => {
    await harness.removePlot(0);
    await harness.removePlot(0);
    expect(await harness.getPlotCount()).toBe(2);

    await harness.clickResetPlotsToDefault();
    expect(await harness.getPlotCount()).toBe(4);
  });

  it('should add and remove comparison experiments', async () => {
    component.newComparisonForm.patchValue({
      xid: 234911123,
      wid: 1,
      title: 'VCC tuned_v1',
    });
    await harness.clickAddComparison();

    expect(await harness.getComparisonCount()).toBe(1);
    expect(component.comparisons()[0].xid).toBe(234911123);
    const comparisonItems = await harness.getComparisonItems();
    expect(await comparisonItems[0].getText()).toContain('234911123');

    await harness.removeComparison(0);
    expect(await harness.getComparisonCount()).toBe(0);
  });

  it('should render "Add Experiment" on the comparison button and add comparison when clicked', async () => {
    expect(await harness.getAddComparisonButtonText()).toContain('Add Experiment');

    component.newComparisonForm.patchValue({
      xid: 234911123,
      wid: 1,
      title: 'VCC tuned_v1',
    });

    expect(await harness.isAddComparisonButtonDisabled()).toBeFalse();
    await harness.clickAddComparison();

    expect(await harness.getComparisonCount()).toBe(1);
    expect(component.comparisons()[0].xid).toBe(234911123);
  });

  it('should not add comparison experiment when form is invalid or WID is null', async () => {
    component.newComparisonForm.patchValue({
      xid: -1,
      wid: 1,
    });
    await harness.clickAddComparison();
    expect(await harness.getComparisonCount()).toBe(0);

    component.newComparisonForm.patchValue({
      xid: 234911123,
      wid: null,
    });
    expect(component.newComparisonForm.invalid).toBeTrue();
    expect(await harness.isAddComparisonButtonDisabled()).toBeTrue();
    await harness.clickAddComparison();
    expect(await harness.getComparisonCount()).toBe(0);
  });

  it('should be invalid when baseWid is null', () => {
    component.form.patchValue({
      title: 'Benchmark',
      baseXid: 234911122,
      baseWid: null,
    });
    expect(component.form.invalid).toBeTrue();
  });

  it('should return the dashboard draft and the charts to provision on submit', async () => {
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
    await harness.clickAddComparison();

    expect(component.form.valid).toBeTrue();
    expect(await harness.isSubmitButtonDisabled()).toBeFalse();
    await harness.clickSubmit();

    expect(dialogRefSpy.close).toHaveBeenCalledTimes(1);
    const result = dialogRefSpy.close.calls.mostRecent().args[0] as CreateDashboardDialogResult;

    const want = {
      dashboard: jasmine.objectContaining({
        title: 'Custom Evaluation Benchmark',
        description: 'Benchmark dashboard comparing tuning iterations',
        chartLayout: { type: LayoutType.SIDE_BY_SIDE },
      }),
      charts: [
        jasmine.objectContaining({ xid: 234911122, wid: 1, title: 'VCC Baseline' }),
        jasmine.objectContaining({ xid: 234911123, wid: 1, title: 'VCC tuned_v1' }),
      ],
    };

    expect(result.dashboard).toEqual(want.dashboard);
    expect(result.charts).toEqual(want.charts);
    // Only the count is asserted here; the plot list itself is exercised by the add, remove and
    // reset tests above.
    expect(result.charts[0].plots?.length).toBe(4);
  });

  it('should leave resource names unset so the server assigns them', async () => {
    component.form.patchValue({
      title: 'Custom Evaluation Benchmark',
      baseXid: 234911122,
      baseWid: 1,
    });

    await harness.clickSubmit();

    const result = dialogRefSpy.close.calls.mostRecent().args[0] as CreateDashboardDialogResult;
    expect(result.dashboard.name).toBeUndefined();
    expect(result.dashboard.chartIds).toBeUndefined();
    expect(result.charts.every((chart) => chart.name === undefined)).toBeTrue();
  });

  it('should close without value on cancel', async () => {
    await harness.clickCancel();
    expect(dialogRefSpy.close).toHaveBeenCalledWith();
  });
});
