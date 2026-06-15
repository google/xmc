// Copyright 2025 Google LLC
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
import { ChangeDetectionStrategy, Component, input, output } from '@angular/core';
import { MatOptionModule } from '@angular/material/core';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatSelectChange, MatSelectModule } from '@angular/material/select';
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
