import {CommonModule} from '@angular/common';
import {ChangeDetectionStrategy, Component, inject, output} from '@angular/core';
import {FormControl, FormsModule, ReactiveFormsModule} from '@angular/forms';
import {MatButtonModule} from '@angular/material/button';
import {MatCardModule} from '@angular/material/card';
import {MatFormFieldModule} from '@angular/material/form-field';
import {MatIconModule} from '@angular/material/icon';
import {MatInputModule} from '@angular/material/input';
import {MatMenuModule} from '@angular/material/menu';
import {MatToolbarModule} from '@angular/material/toolbar';
import {MatTooltipModule} from '@angular/material/tooltip';
import {Router, RouterLink} from '@angular/router';

import {experimentFiltersToQueryParams} from '../../../experiments_list_page/utils/query_params';

/**
 * Header component for the XMC UI.
 */
@Component({
  selector: 'xmc-header',
  standalone: true,
  templateUrl: './header.ng.html',
  styleUrl: './header.css',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    ReactiveFormsModule,
    FormsModule,
    RouterLink,
    MatToolbarModule,
    MatButtonModule,
    MatIconModule,
    MatInputModule,
    MatMenuModule,
    MatFormFieldModule,
    MatTooltipModule,
    MatCardModule,
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
