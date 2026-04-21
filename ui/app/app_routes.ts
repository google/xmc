import {Routes} from '@angular/router';

import {ExperimentPage} from './experiment_page/experiment_page';
import {ExperimentsListPage} from './experiments_list_page/experiments_list_page';
import {WorkUnitPage} from './work_unit_page/work_unit_page';

/**
 * The application routes.
 */
export const routes: Routes = [
  {
    path: '',
    redirectTo: 'experiments',
    pathMatch: 'full',
  },
  {
    path: 'experiments',
    component: ExperimentsListPage,
  },
  {
    path: 'experiments/:xid',
    component: ExperimentPage,
  },
  {
    path: 'experiments/:xid/workUnits/:wid',
    component: WorkUnitPage,
  },
];
