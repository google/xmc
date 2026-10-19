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

import { CommonModule } from '@angular/common';
import {
  ChangeDetectionStrategy,
  Component,
  computed,
  inject,
  OnInit,
  signal,
} from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatDialog, MatDialogModule } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatTableModule } from '@angular/material/table';
import { Router, RouterLink } from '@angular/router';

import {
  CreateDashboardDialogComponent,
  CreateDashboardDialogResult,
} from 'app/common/components/dashboard/create_dashboard_dialog/create_dashboard_dialog';
import { PageSizeSelector } from 'app/common/components/page_size_selector/page_size_selector';
import {
  getInitialPaginationState,
  goToNextPage,
  goToPreviousPage,
  PaginationState,
  setPageSize,
} from 'app/common/pagination/pagination';
import { extractDashboardId } from 'app/common/resource_name_extractor';
import { Dashboard } from 'app/service/dashboard_models';
import { DashboardService } from 'app/service/dashboard_service';
import { SnackbarService } from 'app/service/snackbar_service';

/** Default page size requested when listing dashboards. */
const DEFAULT_PAGE_SIZE = 10;

/**
 * Component displaying the list of available metrics dashboards with pagination controls.
 */
@Component({
  selector: 'xmc-dashboards-list-page',
  standalone: true,
  templateUrl: './dashboards_list_page.ng.html',
  styleUrl: './dashboards_list_page.scss',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    FormsModule,
    RouterLink,
    MatButtonModule,
    MatCardModule,
    MatDialogModule,
    MatIconModule,
    MatInputModule,
    MatFormFieldModule,
    MatProgressBarModule,
    MatTableModule,
    PageSizeSelector,
  ],
})
export class DashboardsListPage implements OnInit {
  private readonly dashboardService = inject(DashboardService);
  private readonly dialog = inject(MatDialog);
  private readonly router = inject(Router);
  private readonly snackbar = inject(SnackbarService, { optional: true });

  readonly dashboards = signal<Dashboard[]>([]);
  readonly isLoading = signal<boolean>(false);
  readonly searchQuery = signal<string>('');
  readonly displayedColumns: string[] = ['title', 'description', 'chartsCount', 'actions'];

  readonly pagination = signal<PaginationState>(getInitialPaginationState(DEFAULT_PAGE_SIZE));

  readonly pageSize = computed(() => this.pagination().pageSize);
  readonly pageIndex = computed(() => this.pagination().pageIndex);
  readonly hasNextPage = computed(() => Boolean(this.pagination().nextPageToken));
  readonly hasPreviousPage = computed(() => this.pagination().previousTokens.length > 0);

  readonly filteredDashboards = computed(() => {
    const query = this.searchQuery().trim().toLowerCase();
    if (!query) return this.dashboards();

    return this.dashboards().filter((d) => {
      const matchTitle = (d.title || '').toLowerCase().includes(query);
      const matchDesc = (d.description || '').toLowerCase().includes(query);
      const matchName = (d.name || '').toLowerCase().includes(query);
      return matchTitle || matchDesc || matchName;
    });
  });

  ngOnInit(): void {
    this.loadDashboards();
  }

  loadDashboards(): void {
    this.isLoading.set(true);
    const { pageSize, currentPageToken } = this.pagination();
    this.dashboardService
      .listDashboards({
        pageSize,
        pageToken: currentPageToken || undefined,
      })
      .subscribe({
        next: (res) => {
          this.dashboards.set(res.dashboards ?? []);
          this.pagination.update((prev) => ({
            ...prev,
            nextPageToken: res.nextPageToken,
          }));
          this.isLoading.set(false);
        },
        error: (err) => {
          this.isLoading.set(false);
          this.snackbar?.open?.('Failed to load dashboards');
          console.error('Error loading dashboards:', err);
        },
      });
  }

  refresh(): void {
    this.pagination.set(getInitialPaginationState(this.pagination().pageSize));
    this.loadDashboards();
  }

  onPageSizeChange(newPageSize: number): void {
    this.pagination.set(setPageSize({ pagination: this.pagination() }, newPageSize).pagination);
    this.loadDashboards();
  }

  goToNextPage(): void {
    if (!this.hasNextPage() || this.isLoading()) {
      return;
    }
    this.pagination.set(goToNextPage({ pagination: this.pagination() }).pagination);
    this.loadDashboards();
  }

  goToPreviousPage(): void {
    if (!this.hasPreviousPage() || this.isLoading()) {
      return;
    }
    this.pagination.set(goToPreviousPage({ pagination: this.pagination() }).pagination);
    this.loadDashboards();
  }

  getDashboardId(name?: string): string {
    return extractDashboardId(name);
  }

  openCreateDashboardDialog(): void {
    const dialogRef = this.dialog.open(CreateDashboardDialogComponent, {
      width: '820px',
      maxWidth: '95vw',
      disableClose: false,
    });

    dialogRef.afterClosed().subscribe((result?: CreateDashboardDialogResult) => {
      if (!result) return;
      this.createDashboard(result);
    });
  }

  /**
   * Creates the dashboard and the charts the dialog configured for it.
   *
   * Both go out in one CreateDashboard call: the server assigns the chart IDs, links them and
   * commits everything in a single transaction, so a partial failure cannot leave behind a
   * dashboard that renders empty or charts that nothing points at.
   */
  createDashboard(result: CreateDashboardDialogResult): void {
    this.isLoading.set(true);
    this.dashboardService
      .createDashboard({ dashboard: result.dashboard, charts: result.charts })
      .subscribe({
        next: (created) => {
          this.isLoading.set(false);
          const id = extractDashboardId(created.name);
          if (id) {
            this.router.navigate(['/dashboards', id]);
            return;
          }
          this.refresh();
        },
        error: (err) => {
          this.isLoading.set(false);
          this.snackbar?.open?.('Failed to create dashboard');
          console.error('Error creating dashboard:', err);
        },
      });
  }
}
