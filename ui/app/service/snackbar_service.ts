import {inject, Injectable} from '@angular/core';
import {MatSnackBar} from '@angular/material/snack-bar';

/**
 * Common service to open snackbars.
 */
@Injectable({providedIn: 'root'})
export class SnackbarService {
  private readonly snackBar = inject(MatSnackBar);

  open(
      message: string,
      action = 'Close',
  ) {
    this.snackBar.open(message, action);
  }
}
