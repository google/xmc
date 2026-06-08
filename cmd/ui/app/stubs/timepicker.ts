// Custom local stub for MatTimepickerModule
import { Component, Directive, Input, NgModule } from '@angular/core';

@Component({
  selector: 'mat-timepicker',
  standalone: true,
  template: '',
})
export class MatTimepicker {}

@Component({
  selector: 'mat-timepicker-toggle',
  standalone: true,
  template: '',
})
export class MatTimepickerToggle {
  @Input() for: any;
}

@Directive({
  selector: '[matTimepicker]',
  standalone: true,
})
export class MatTimepickerInput {
  @Input() matTimepicker: any;
}

@NgModule({
  imports: [MatTimepicker, MatTimepickerToggle, MatTimepickerInput],
  exports: [MatTimepicker, MatTimepickerToggle, MatTimepickerInput],
})
export class MatTimepickerModule {}
