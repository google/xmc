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
import {
  ChangeDetectionStrategy,
  Component,
  computed,
  inject,
  input,
  OnDestroy,
} from '@angular/core';
import { toObservable } from '@angular/core/rxjs-interop';
import {
  AbstractControl,
  FormControl,
  FormGroup,
  FormsModule,
  ReactiveFormsModule,
  ValidationErrors,
  Validators,
} from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatButtonToggleModule } from '@angular/material/button-toggle';
import { MatCheckboxModule } from '@angular/material/checkbox';
import { MatRippleModule } from '@angular/material/core';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatProgressSpinner } from '@angular/material/progress-spinner';
import { MatRadioModule } from '@angular/material/radio';
import { MatSelectModule } from '@angular/material/select';
import { Subscription } from 'rxjs';

import { Experiment } from 'xmanager_cloud/experiment_state_server/proto/experiment.jsonpb_decls';
import { Priority } from 'xmanager_cloud/experiment_state_server/proto/priority.jsonpb_decls';
import { XidBadge } from 'xmanager_cloud/ui/app/common/components/xid_badge/xid_badge';
import {
  BaseSidePanelContent,
  BaseSidePanelData,
} from 'xmanager_cloud/ui/app/side_panel/base_side_panel';
import { SidePanelService } from 'xmanager_cloud/ui/app/side_panel/side_panel_service';

import { EditExperimentFormStore } from './edit_experiment_form_store';

/** Represents the data to render the Edit Experiment side panel. */
export class EditExperimentFormData implements BaseSidePanelData {
  constructor(
    public xid: string,
    public onUpdateSuccess?: () => void,
    public onUpdateStart?: () => void,
    public onUpdateFinish?: () => void,
  ) {}
}

function notWhitespaceOnly(control: AbstractControl): ValidationErrors | null {
  const isWhitespace = (control.value || '').trim().length === 0;
  return isWhitespace ? { whitespace: true } : null;
}

/** Side panel content for editing an experiment. */
@Component({
  selector: 'xmc-edit-experiment-form',
  standalone: true,
  templateUrl: './edit_experiment_form.ng.html',
  styleUrl: './edit_experiment_form.css',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [
    CommonModule,
    FormsModule,
    ReactiveFormsModule,
    MatIconModule,
    MatCheckboxModule,
    MatButtonToggleModule,
    MatButtonModule,
    MatFormFieldModule,
    MatInputModule,
    MatRadioModule,
    MatRippleModule,
    MatSelectModule,
    MatProgressSpinner,
    XidBadge,
  ],
  providers: [EditExperimentFormStore],
})
export class EditExperimentForm implements BaseSidePanelContent<EditExperimentFormData>, OnDestroy {
  readonly data = input.required<EditExperimentFormData>();

  private readonly editExperimentFormStore = inject(EditExperimentFormStore);
  private readonly sidePanelService = inject(SidePanelService);
  private readonly subscriptions = new Subscription();

  readonly initialExperiment = this.editExperimentFormStore.experiment;
  readonly isLoading = this.editExperimentFormStore.isLoading;

  readonly editExperimentFormGroup = computed(() => {
    return createExperimentForm(this.initialExperiment());
  });

  constructor() {
    this.subscriptions.add(
      toObservable(this.data).subscribe((data) => {
        this.editExperimentFormStore.fetchExperiment(data.xid);
      }),
    );
  }

  close() {
    this.sidePanelService.closeSidePanel();
  }

  updateExperiment() {
    const { updatedExperiment, updateMask } = this.getExperimentUpdates();

    this.data().onUpdateStart?.();
    this.editExperimentFormStore.updateExperiment({
      request: {
        experiment: updatedExperiment,
        updateMask,
      },
      onSuccess: this.data().onUpdateSuccess,
      onFinish: this.data().onUpdateFinish,
    });
    this.sidePanelService.closeSidePanel();
  }

  private getExperimentUpdates(): { updatedExperiment: Experiment; updateMask: string } {
    const formGroup = this.editExperimentFormGroup();
    const initial = this.initialExperiment()!;
    const { updates, updateMask } = getDirtyFields(formGroup);

    return {
      updatedExperiment: { ...initial, ...updates },
      updateMask: updateMask.join(','),
    };
  }

  ngOnDestroy() {
    this.subscriptions.unsubscribe();
  }
}

function createExperimentForm(experiment: Experiment | null | undefined): FormGroup {
  const values = getFormValues(experiment);
  return new FormGroup({
    name: new FormControl<string>(values.name, [Validators.required, notWhitespaceOnly]),
    notes: new FormControl<string>(values.notes),
    tags: new FormControl<string>(values.tags),
    priority: new FormControl<Priority>(values.priority),
  });
}

function getFormValues(experiment: Experiment | null | undefined) {
  if (!experiment) {
    return { name: '', notes: '', tags: '', priority: 'NORMAL' as Priority };
  }
  return {
    name: fallback(experiment.title, ''),
    notes: fallback(experiment.notes, ''),
    tags: getTagsString(experiment.tags),
    priority: fallback(experiment.priority, 'NORMAL' as Priority),
  };
}

function getTagsString(tags: string[] | undefined): string {
  return tags ? tags.join(',') : '';
}

function fallback<T>(val: T | undefined | null, def: T): T {
  return val != null ? val : def;
}

function getDirtyFields(formGroup: FormGroup): {
  updates: Partial<Experiment>;
  updateMask: string[];
} {
  const updates: Partial<Experiment> = {};
  const updateMask: string[] = [];
  const { name, notes, tags, priority } = formGroup.controls;

  if (name.dirty) {
    updates.title = name.value;
    updateMask.push('title');
  }
  if (notes.dirty) {
    updates.notes = notes.value;
    updateMask.push('notes');
  }
  if (tags.dirty) {
    updates.tags = parseTags(tags.value);
    updateMask.push('tags');
  }
  if (priority.dirty) {
    updates.priority = priority.value;
    updateMask.push('priority');
  }
  return { updates, updateMask };
}

function parseTags(tagsValue: string | null): string[] {
  if (!tagsValue) {
    return [];
  }
  return tagsValue
    .split(',')
    .map((tag) => tag.trim())
    .filter((tag) => tag !== '');
}
