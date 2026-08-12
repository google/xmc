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

import { ChangeDetectionStrategy, Component, input, OnInit, output, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';

export interface ResourceRequirementRow {
  id: string;
  amount: number;
  resourceType: string;
}

export interface ResourceRequirementQuery {
  requirements: ResourceRequirementRow[];
}

let nextRowId = 0;

function createRow(amount = 1, resourceType = ''): ResourceRequirementRow {
  return { id: `row-${++nextRowId}`, amount, resourceType };
}

@Component({
  selector: 'xmc-smart-resource-finder',
  standalone: true,
  templateUrl: './smart_resource_finder.ng.html',
  styleUrl: './smart_resource_finder.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [FormsModule, MatIconModule, MatButtonModule],
})
export class SmartResourceFinderComponent implements OnInit {
  readonly availableResourceTypes = input<string[]>([]);
  readonly findMatchingQueues = output<ResourceRequirementQuery>();

  protected readonly rows = signal<ResourceRequirementRow[]>([createRow(1, '')]);

  ngOnInit() {
    this.emitQuery();
  }

  protected addRow() {
    this.rows.update((r) => [...r, createRow(1, '')]);
    this.emitQuery();
  }

  protected removeRow(index: number) {
    if (this.rows().length <= 1) return;
    this.rows.update((r) => r.filter((_, i) => i !== index));
    this.emitQuery();
  }

  protected updateRowAmount(index: number, val: number | null | undefined) {
    const amountNum = typeof val === 'number' ? val : Number(val);
    this.rows.update((r) => {
      const copy = [...r];
      copy[index] = { ...copy[index], amount: amountNum };
      return copy;
    });
    this.emitQuery();
  }

  protected updateRowResourceType(index: number, val: string) {
    this.rows.update((r) => {
      const copy = [...r];
      copy[index] = { ...copy[index], resourceType: val };
      return copy;
    });
    this.emitQuery();
  }

  private emitQuery() {
    const validRows = this.rows().filter(
      (row) => row.amount != null && !isNaN(row.amount) && row.amount > 0,
    );
    this.findMatchingQueues.emit({
      requirements: validRows,
    });
  }
}
