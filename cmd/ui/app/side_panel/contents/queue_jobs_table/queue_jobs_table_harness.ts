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

/** Harness for interacting with individual rows inside <xmc-queue-jobs-table>. */
export class QueueJobsTableRowHarness extends ComponentHarness {
  static hostSelector = 'tr.mat-mdc-row';

  private readonly positionCell = this.locatorForOptional('.position-cell');
  private readonly nameCell = this.locatorForOptional('.name-cell');
  private readonly statusCell = this.locatorForOptional('.status-cell');
  private readonly priorityCell = this.locatorForOptional('.priority-cell');
  private readonly workunitCell = this.locatorForOptional('.workunit-cell');
  private readonly authorCell = this.locatorForOptional('.author-cell');

  async getCellsText(): Promise<{
    position: string;
    jobName: string;
    status: string;
    priority: string;
    workUnitId: string;
    user: string;
  }> {
    const pos = await this.positionCell();
    const name = await this.nameCell();
    const status = await this.statusCell();
    const priority = await this.priorityCell();
    const workunit = await this.workunitCell();
    const author = await this.authorCell();

    return {
      position: pos ? await pos.text() : '',
      jobName: name ? await name.text() : '',
      status: status ? await status.text() : '',
      priority: priority ? await priority.text() : '',
      workUnitId: workunit ? await workunit.text() : '',
      user: author ? await author.text() : '',
    };
  }
}

/** Harness for interacting with <xmc-queue-jobs-table> in tests. */
export class QueueJobsTableHarness extends ComponentHarness {
  static hostSelector = 'xmc-queue-jobs-table';

  private readonly titleElement = this.locatorForOptional('.jobs-card-title');
  private readonly subtitleElement = this.locatorForOptional('.jobs-card-subtitle');
  private readonly targetQueueNameElement = this.locatorForOptional('.target-queue-name');
  private readonly headerCells = this.locatorForAll('th.mat-mdc-header-cell');
  private readonly tableRows = this.locatorForAll(QueueJobsTableRowHarness);
  private readonly paginationInfoElement = this.locatorForOptional('.pagination-info');
  private readonly navButtons = this.locatorForAll('.pagination-nav button');

  /** Gets the card title text if present. */
  async getTitleText(): Promise<string | null> {
    const el = await this.titleElement();
    return el ? el.text() : null;
  }

  /** Gets the card subtitle text if present. */
  async getSubtitleText(): Promise<string | null> {
    const el = await this.subtitleElement();
    return el ? el.text() : null;
  }

  /** Gets the target queue name text if present. */
  async getTargetQueueName(): Promise<string | null> {
    const el = await this.targetQueueNameElement();
    return el ? el.text() : null;
  }

  /** Gets an array of header cell texts. */
  async getHeaderCellTexts(): Promise<string[]> {
    const cells = await this.headerCells();
    return Promise.all(cells.map((c) => c.text()));
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
    priority: string;
    workUnitId: string;
    user: string;
  }> {
    const rows = await this.tableRows();
    if (!rows[rowIndex]) {
      throw new Error(`Row index ${rowIndex} out of bounds.`);
    }
    return rows[rowIndex].getCellsText();
  }

  /** Gets the pagination info string (e.g. "1-10 of 20"). */
  async getPaginationInfoText(): Promise<string | null> {
    const el = await this.paginationInfoElement();
    return el ? el.text() : null;
  }

  /** Clicks the first page navigation button. */
  async clickFirstPage(): Promise<void> {
    const btns = await this.navButtons();
    if (btns[0]) await btns[0].click();
  }

  /** Clicks the previous page navigation button. */
  async clickPreviousPage(): Promise<void> {
    const btns = await this.navButtons();
    if (btns[1]) await btns[1].click();
  }

  /** Clicks the next page navigation button. */
  async clickNextPage(): Promise<void> {
    const btns = await this.navButtons();
    if (btns[2]) await btns[2].click();
  }

  /** Clicks the last page navigation button. */
  async clickLastPage(): Promise<void> {
    const btns = await this.navButtons();
    if (btns[3]) await btns[3].click();
  }
}
