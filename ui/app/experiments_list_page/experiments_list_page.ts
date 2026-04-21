import {ChangeDetectionStrategy, Component, inject, OnDestroy, OnInit} from '@angular/core';
import {MatButtonModule} from '@angular/material/button';
import {MatCardModule} from '@angular/material/card';
import {MatIconModule} from '@angular/material/icon';
import {Sort} from '@angular/material/sort';
import {ActivatedRoute} from '@angular/router';
import {Subscription} from 'rxjs';

import {AutoReloadService, ReloaderControl} from '../common/components/autoreloader/autoreloader_service';
import {PageSizeSelector} from '../common/components/page_size_selector/page_size_selector';

import {ExperimentsListPageStore} from './experiments_list_page_store';
import {ExperimentsTable} from './experiments_table/experiments_table';
import {queryParamsToExperimentFilters} from './utils/query_params';

/**
 * Experiments list page for the XMC UI.
 */
@Component({
  selector: 'xmc-experiments-list-page',
  standalone: true,
  templateUrl: './experiments_list_page.ng.html',
  styleUrl: './experiments_list_page.css',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    MatCardModule,
    MatIconModule,
    MatButtonModule,
    ExperimentsTable,
    PageSizeSelector,
  ],
  providers: [ExperimentsListPageStore],
})
export class ExperimentsListPage implements OnInit, OnDestroy {
  private readonly route = inject(ActivatedRoute);
  private readonly componentStore = inject(ExperimentsListPageStore);
  private readonly autoReloadService = inject(AutoReloadService);
  private readonly reloader: ReloaderControl =
      this.autoReloadService.createReloader({emitOnStart: false});

  private readonly subscriptions = new Subscription();

  readonly pageResult = this.componentStore.pageResult;
  readonly isLoading = this.componentStore.isLoading;

  readonly pageIndex = this.componentStore.pageIndex;
  readonly pageSize = this.componentStore.pageSize;
  readonly hasNextPage = this.componentStore.hasNextPage;
  readonly hasPreviousPage = this.componentStore.hasPreviousPage;

  ngOnInit() {
    this.subscriptions.add(
        this.route.queryParams.subscribe((queryParams) => {
          this.componentStore.setFilter(
              queryParamsToExperimentFilters(queryParams));
          this.componentStore.fetchPage();
        }),
    );
    this.subscriptions.add(
        this.reloader.reloader$.subscribe(() => {
          this.componentStore.fetchPage();
        }),
    );
    this.reloader.start();
  }

  goToPreviousPage() {
    this.componentStore.goToPreviousPage();
    this.componentStore.fetchPage();
  }

  goToNextPage() {
    this.componentStore.goToNextPage();
    this.componentStore.fetchPage();
  }

  onSortChange(sort: Sort) {
    const orderBy =
        sort.direction ? `${sort.active} ${sort.direction}` : 'created desc';
    this.componentStore.setOrderBy(orderBy);
    this.componentStore.fetchPage();
  }

  ngOnDestroy() {
    this.subscriptions.unsubscribe();
    this.reloader.stop();
  }

  onPageSizeChange(pageSize: number) {
    this.componentStore.setPageSize(pageSize);
    this.componentStore.fetchPage();
  }
}