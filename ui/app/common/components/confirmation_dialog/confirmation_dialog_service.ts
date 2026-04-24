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
import {Injectable} from '@angular/core';
import {MatDialog} from '@angular/material/dialog';
import {Observable} from 'rxjs';

import {ConfirmationDialog} from './confirmation_dialog';

/**
 * Service for opening confirmation dialogs.
 */
@Injectable({providedIn: 'root'})
export class ConfirmDialogService {
  constructor(private readonly dialog: MatDialog) {}

  /**
   * Opens a confirmation dialog.
   * @param data Includes the title and message to display in the dialog.
   * @return An observable that emits true if the user confirms the dialog,
   *     false if the user cancels the dialog, or nothing if the user closes the
   *     dialog without confirming or canceling.
   */
  open(data: {title: string, message: string}): Observable<boolean> {
    const {title, message} = data;
    const dialogRef = this.dialog.open(
        ConfirmationDialog, {width: '400px', data: {title, message}});
    return dialogRef.afterClosed();
  }
}
