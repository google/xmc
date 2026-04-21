import {ComponentHarness} from '@angular/cdk/testing';
import {MatSelectHarness} from '@angular/material/select/testing';

/** Harness for interacting with <xmc-status-messages-list-filter> in tests. */
export class StatusMessagesListFilterHarness extends ComponentHarness {
  static hostSelector = 'xmc-status-messages-list-filter';

  private readonly severitySelect = this.locatorFor(
      MatSelectHarness.with({selector: '[data-testid="severity-select"]'}));
  private readonly sourceSelect = this.locatorFor(
      MatSelectHarness.with({selector: '[data-testid="source-select"]'}));
  private readonly sortOrderSelect = this.locatorFor(
      MatSelectHarness.with({selector: '[data-testid="sort-order-select"]'}));

  async selectSeverity(option: string): Promise<void> {
    const select = await this.severitySelect();
    await select.open();
    await select.clickOptions({text: option});
  }

  async selectSource(option: string): Promise<void> {
    const select = await this.sourceSelect();
    await select.open();
    await select.clickOptions({text: option});
  }

  async selectOnlySeverity(option: string): Promise<void> {
    await this.selectOnly(await this.severitySelect(), option);
  }

  async selectOnlySource(option: string): Promise<void> {
    await this.selectOnly(await this.sourceSelect(), option);
  }

  async selectSortOrder(option: string): Promise<void> {
    const select = await this.sortOrderSelect();
    await select.open();
    await select.clickOptions({text: option});
  }

  async getSeverityValue(): Promise<string> {
    const select = await this.severitySelect();
    return select.getValueText();
  }

  async getSourceValue(): Promise<string> {
    const select = await this.sourceSelect();
    return select.getValueText();
  }

  async getSortOrderValue(): Promise<string> {
    const select = await this.sortOrderSelect();
    return select.getValueText();
  }

  private async selectOnly(select: MatSelectHarness, option: string):
      Promise<void> {
    await select.open();
    const options = await select.getOptions();
    const states = await Promise.all(options.map(async o => ({
      text: await o.getText(),
      isSelected: await o.isSelected(),
      option: o
    })));

    for (const s of states) {
      if (s.text === option) {
        if (!s.isSelected) {
          await s.option.click();
        }
      } else {
        if (s.isSelected) {
          await s.option.click();
        }
      }
    }
  }
}
