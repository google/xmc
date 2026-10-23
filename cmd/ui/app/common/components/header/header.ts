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
import { CommonModule } from '@angular/common';
import { ChangeDetectionStrategy, Component, inject, output } from '@angular/core';
import { FormControl, ReactiveFormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatMenuModule } from '@angular/material/menu';
import { MatToolbarModule } from '@angular/material/toolbar';
import { Router, RouterLink } from '@angular/router';

import { experimentFiltersToQueryParams } from 'app/experiments_list_page/query_params';

/**
 * Header component for the XMC UI.
 */
@Component({
  selector: 'xmc-header',
  standalone: true,
  templateUrl: './header.ng.html',
  styleUrl: './header.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    ReactiveFormsModule,
    RouterLink,
    MatToolbarModule,
    MatButtonModule,
    MatIconModule,
    MatInputModule,
    MatMenuModule,
    MatFormFieldModule,
  ],
})
export class Header {
  readonly userInput = new FormControl<string>('');
  readonly menuToggle = output<void>();

  private readonly router = inject(Router);

  searchExperiments() {
    this.router.navigate(['/experiments'], {
      queryParams: experimentFiltersToQueryParams(this.userInput.value || ''),
    });
  }

  toggleSidenav() {
    this.menuToggle.emit();
  }
}
