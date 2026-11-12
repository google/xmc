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

import { ComponentHarness } from '@angular/cdk/testing';
import { MatButtonHarness } from '@angular/material/button/testing';
import { MatInputHarness } from '@angular/material/input/testing';
import { MatPaginatorHarness } from '@angular/material/paginator/testing';
import { MatSelectHarness } from '@angular/material/select/testing';
import { MatSortHeaderHarness } from '@angular/material/sort/testing';
import { MatTooltipHarness } from '@angular/material/tooltip/testing';

import { QueueUserAllocationBarHarness } from './queue_user_allocation_bar/queue_user_allocation_bar_harness';

/** Harness for interacting with individual rows inside <xmc-queue-jobs-table>. */
export class QueueJobsTableRowHarness extends ComponentHarness {
  static hostSelector = 'tr.mat-mdc-row';

  private readonly positionCell = this.locatorForOptional('.position-cell');
  private readonly nameCell = this.locatorForOptional('.name-cell');
  private readonly statusCell = this.locatorForOptional('.status-cell');
  private readonly runningTimeCell = this.locatorForOptional('.running-time-cell');
  private readonly priorityCell = this.locatorForOptional('.priority-cell');
  private readonly resourcesCell = this.locatorForOptional('.resources-cell');
  private readonly workunitCell = this.locatorForOptional('.workunit-cell');
  private readonly authorCell = this.locatorForOptional('.author-cell');
  private readonly jobLink = this.locatorForOptional('.job-link');
  private readonly workunitLink = this.locatorForOptional('.workunit-link');

  async getCellsText(): Promise<{
    position: string;
    jobName: string;
    status: string;
    runningTime: string;
    priority: string;
    resources: string;
    workUnitId: string;
    user: string;
  }> {
    const pos = await this.positionCell();
    const name = await this.nameCell();
    const status = await this.statusCell();
    const runningTime = await this.runningTimeCell();
    const priority = await this.priorityCell();
    const res = await this.resourcesCell();
    const workunit = await this.workunitCell();
    const author = await this.authorCell();

    return {
      position: pos ? await pos.text() : '',
      jobName: name ? await name.text() : '',
      status: status ? await status.text() : '',
      runningTime: runningTime ? await runningTime.text() : '',
      priority: priority ? await priority.text() : '',
      resources: res ? await res.text() : '',
      workUnitId: workunit ? await workunit.text() : '',
      user: author ? await author.text() : '',
    };
  }

  async getLinks(): Promise<{
    jobLink: string | null;
    jobLinkAriaLabel: string | null;
    workUnitLink: string | null;
  }> {
    const jobEl = await this.jobLink();
    const wuEl = await this.workunitLink();
    return {
      jobLink: jobEl ? await jobEl.getAttribute('href') : null,
      jobLinkAriaLabel: jobEl ? await jobEl.getAttribute('aria-label') : null,
      workUnitLink: wuEl ? await wuEl.getAttribute('href') : null,
    };
  }
}

/** Harness for interacting with <xmc-queue-jobs-table> in tests. */
export class QueueJobsTableHarness extends ComponentHarness {
  static hostSelector = 'xmc-queue-jobs-table';

  async filterAuthor(author: string): Promise<void> {
    const input = await this.locatorFor(MatInputHarness)();
    await input.setValue(author);
    await (await input.host()).dispatchEvent('change');
  }

  async filterStatus(status: string): Promise<void> {
    const select = await this.locatorFor(MatSelectHarness.with({ ancestor: '.job-filters' }))();
    await select.open();
    await select.clickOptions({ text: status });
  }

  async clearFilters(): Promise<void> {
    await (await this.locatorFor(MatButtonHarness.with({ text: 'Clear filters' }))()).click();
  }

  async sortBy(label: string): Promise<void> {
    const header = await this.locatorFor(MatSortHeaderHarness.with({ label }))();
    await header.click();
  }

  private readonly headerCells = this.locatorForAll('th.mat-mdc-header-cell');
  private readonly tableRows = this.locatorForAll(QueueJobsTableRowHarness);
  private readonly paginator = this.locatorForOptional(MatPaginatorHarness);
  private readonly headerActions = this.locatorForAll('.title-row > [headerActions]');
  private readonly userAllocationBar = this.locatorForOptional(QueueUserAllocationBarHarness);

  /** Gets the per-user allocation bar rendered above the jobs table, or null while jobs load. */
  async getUserAllocationBar(): Promise<QueueUserAllocationBarHarness | null> {
    return this.userAllocationBar();
  }

  /** Gets the trimmed text contents of any elements projected into the [headerActions] slot. */
  async getHeaderActionTexts(): Promise<string[]> {
    const actions = await this.headerActions();
    const texts = await Promise.all(actions.map((el) => el.text()));
    return texts.map((text) => text.trim());
  }

  /** Gets the MatPaginatorHarness for the table if present. */
  async getPaginator(): Promise<MatPaginatorHarness | null> {
    return this.paginator();
  }

  /** Gets an array of header cell texts (excluding help icon ligatures). */
  async getHeaderCellTexts(): Promise<string[]> {
    const cells = await this.headerCells();
    const texts = await Promise.all(cells.map((c) => c.text({ exclude: '.help-tip-icon' })));
    return texts.map((text) => text.trim());
  }

  /** Gets the tooltip harness for the Running time header explanation icon if present. */
  async getRunningTimeTooltip(): Promise<MatTooltipHarness | null> {
    return this.locatorForOptional(
      MatTooltipHarness.with({ selector: '.running-time-help-icon' }),
    )();
  }

  /** Gets the total number of table rows currently displayed. */
  async getRowCount(): Promise<number> {
    const rows = await this.tableRows();
    return rows.length;
  }

  /** Gets the text content of all cells in the row at the specified index. */
  async getRowCellsText(rowIndex: number): Promise<{
    position: string;
    jobName: string;
    status: string;
    runningTime: string;
    priority: string;
    resources: string;
    workUnitId: string;
    user: string;
  }> {
    const rows = await this.tableRows();
    if (!rows[rowIndex]) {
      throw new Error(`Row index ${rowIndex} out of bounds.`);
    }
    return rows[rowIndex].getCellsText();
  }

  /** Gets the href and aria-label attributes of the experiment and work unit links in the row at the specified index. */
  async getRowLinks(rowIndex: number): Promise<{
    jobLink: string | null;
    jobLinkAriaLabel: string | null;
    workUnitLink: string | null;
  }> {
    const rows = await this.tableRows();
    if (!rows[rowIndex]) {
      throw new Error(`Row index ${rowIndex} out of bounds.`);
    }
    return rows[rowIndex].getLinks();
  }
}
