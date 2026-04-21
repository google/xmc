import {ChangeDetectionStrategy, Component, input, output} from '@angular/core';
import {MatOptionModule} from '@angular/material/core';
import {MatFormFieldModule} from '@angular/material/form-field';
import {MatSelectChange, MatSelectModule} from '@angular/material/select';
/**
 * Reusable component for selecting page size.
 */
@Component({
  selector: 'xmc-page-size-selector',
  standalone: true,
  imports: [MatFormFieldModule, MatSelectModule, MatOptionModule],
  templateUrl: './page_size_selector.ng.html',
  styleUrl: './page_size_selector.css',
  changeDetection: ChangeDetectionStrategy.OnPush,
})
export class PageSizeSelector {
  /** The currently selected page size. */
  readonly pageSize = input.required<number>();
  /** The list of available page sizes to display in the dropdown. */
  readonly pageSizeOptions = input<number[]>([10, 25, 50, 100]);
  /** Emits the new page size when the selection changes. */
  readonly pageSizeChange = output<number>();
  protected onSelectionChange(event: MatSelectChange): void {
    this.pageSizeChange.emit(event.value);
  }
}
