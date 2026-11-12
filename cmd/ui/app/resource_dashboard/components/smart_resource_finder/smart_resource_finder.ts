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
import { MatOptionModule } from '@angular/material/core';
import { MatIconModule } from '@angular/material/icon';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';

import { ROD_TERM_TOOLTIPS } from 'app/resource_dashboard/constants/rod_tooltips';

export interface ResourceRequirementRow {
  id: string;
  amount: number;
  resourceType: string;
}

export interface ResourceRequirementQuery {
  requirements: ResourceRequirementRow[];
}

function createRow(amount = 1, resourceType = ''): ResourceRequirementRow {
  return { id: crypto.randomUUID(), amount, resourceType };
}

@Component({
  selector: 'xmc-smart-resource-finder',
  standalone: true,
  templateUrl: './smart_resource_finder.ng.html',
  styleUrl: './smart_resource_finder.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    FormsModule,
    MatIconModule,
    MatButtonModule,
    MatSelectModule,
    MatOptionModule,
    MatTooltipModule,
  ],
})
export class SmartResourceFinderComponent implements OnInit {
  readonly availableResourceTypes = input<string[]>([]);
  readonly findMatchingQueues = output<ResourceRequirementQuery>();

  protected readonly dominantTooltip = ROD_TERM_TOOLTIPS.dominantResource;
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

  protected getRemoveRowAriaLabel(resourceType: string, index: number): string {
    return `Remove ${resourceType || 'resource filter'} (row ${index + 1})`;
  }

  protected updateRowAmount(index: number, val: number | string | null | undefined) {
    const amountNum = typeof val === 'number' ? val : Number(val);
    const current = this.rows()[index];
    if (current && current.amount === amountNum) return;
    this.rows.update((r) => r.with(index, { ...r[index], amount: amountNum }));
    this.emitQuery();
  }

  protected onResourceTypeChange(index: number, val: string) {
    const current = this.rows()[index];
    if (current && current.resourceType === val) return;
    this.rows.update((r) => r.with(index, { ...r[index], resourceType: val }));
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
