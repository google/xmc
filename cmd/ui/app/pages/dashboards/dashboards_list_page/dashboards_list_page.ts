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

import { CreateDashboardDialogComponent } from 'xmanager_cloud/ui/app/common/components/dashboard/create_dashboard_dialog/create_dashboard_dialog';
import { Dashboard } from 'xmanager_cloud/ui/app/service/dashboard_models';
import { DashboardService } from 'xmanager_cloud/ui/app/service/dashboard_service';
import { SnackbarService } from 'xmanager_cloud/ui/app/service/snackbar_service';

/**
 * Page size requested when listing dashboards. This page renders the whole response at once and
 * filters it client-side, so it asks for the server maximum rather than relying on the smaller
 * server-side default, which would silently truncate the list.
 */
const DASHBOARDS_PAGE_SIZE = 100;

/**
 * Strips 'dashboards/' prefix to obtain a clean dashboard ID for routing.
 */
export function extractDashboardId(name?: string): string {
  if (!name) return '';
  return name.startsWith('dashboards/') ? name.replace('dashboards/', '') : name;
}

/**
 * Component displaying the list of available metrics dashboards.
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
    this.dashboardService.listDashboards({ pageSize: DASHBOARDS_PAGE_SIZE }).subscribe({
      next: (res) => {
        this.dashboards.set(res.dashboards ?? []);
        this.isLoading.set(false);
      },
      error: (err) => {
        this.isLoading.set(false);
        this.snackbar?.open?.('Failed to load dashboards');
        console.error('Error loading dashboards:', err);
      },
    });
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

    dialogRef.afterClosed().subscribe((dashboard?: Dashboard) => {
      if (!dashboard) return;
      this.createDashboard(dashboard);
    });
  }

  createDashboard(dashboard: Dashboard): void {
    this.isLoading.set(true);
    this.dashboardService.createDashboard({ dashboard }).subscribe({
      next: (created) => {
        this.isLoading.set(false);
        const id = extractDashboardId(created.name);
        if (id) {
          this.router.navigate(['/dashboards', id]);
          return;
        }
        this.loadDashboards();
      },
      error: () => {
        this.isLoading.set(false);
        this.loadDashboards();
      },
    });
  }
}
