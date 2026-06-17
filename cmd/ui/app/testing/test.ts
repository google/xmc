// Copyright 2026 Google LLC
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

import { TestbedHarnessEnvironment } from '@angular/cdk/testing/testbed';
import { Component } from '@angular/core';
import { getTestBed, TestBed, TestModuleMetadata } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import {
  BrowserDynamicTestingModule,
  platformBrowserDynamicTesting,
} from '@angular/platform-browser-dynamic/testing';

// 1. Initialize the Angular testing environment.
getTestBed().initTestEnvironment(BrowserDynamicTestingModule, platformBrowserDynamicTesting());

let currentFixture: any = null;

beforeEach(() => {
  currentFixture = null;
});

afterEach(() => {
  if (currentFixture) {
    currentFixture.destroy();
    currentFixture = null;
  }
});

// 2. Define cleanState helper
export function cleanState(factory: () => any, beforeEachFn: typeof beforeEach): any {
  const state = {} as any;
  beforeEachFn(async () => {
    const fresh = await factory();
    for (const key of Object.keys(state)) {
      delete state[key];
    }
    Object.assign(state, fresh);
  });
  return state;
}

// 3. Define setupModule helper
export function setupModule(moduleDef: TestModuleMetadata) {
  TestBed.configureTestingModule(moduleDef);
}

// 4. Define bootstrap helper
export function bootstrap(component: any, options?: any): any {
  const fixture = TestBed.createComponent(component);
  if (options) {
    for (const key of Object.keys(options)) {
      try {
        fixture.componentRef.setInput(key, options[key]);
      } catch (e) {
        (fixture.componentInstance as any)[key] = options[key];
      }
    }
  }
  fixture.detectChanges();
  let selector: string | undefined = undefined;
  try {
    const cmpDef = (component as any).ɵcmp;
    if (cmpDef && cmpDef.selectors && cmpDef.selectors[0]) {
      selector = cmpDef.selectors[0][0];
    }
  } catch (e) {
    // Fallback or ignore
  }
  if (selector) {
    fixture.nativeElement.setAttribute('data-component-selector', selector);
  }
  currentFixture = fixture;
  return fixture.componentInstance;
}

// 5. Define get helper
export function get(token: any): any {
  return TestBed.inject(token);
}

// 6. Define getHarness helper
export async function getHarness(query: any): Promise<any> {
  if (!currentFixture) {
    throw new Error(
      'getHarness: No active component fixture has been bootstrapped. Call bootstrap(Component) first.',
    );
  }
  const selector = query.hostSelector;
  const elementPrototype = Element.prototype as any;
  const matchesFn = elementPrototype['matches'] || elementPrototype['msMatchesSelector'];
  const isRootMatch = selector && matchesFn.call(currentFixture.nativeElement, selector);

  if (isRootMatch) {
    try {
      return await TestbedHarnessEnvironment.harnessForFixture(currentFixture, query);
    } catch (e) {
      // Fallback to loader if harnessForFixture fails
    }
  }

  if (!currentFixture) {
    throw new Error('getHarness: Component fixture was destroyed during retrieval.');
  }
  const loader = TestbedHarnessEnvironment.loader(currentFixture);
  return await loader.getHarness(query);
}

// 7. Define DOM query helpers
export function getEl<T extends HTMLElement = HTMLElement>(selector: string): T {
  if (!currentFixture) {
    throw new Error('getEl: No active component fixture has been bootstrapped.');
  }
  return currentFixture.nativeElement.querySelector(selector);
}

export function getDebugEl(token?: any): any {
  if (!currentFixture) {
    throw new Error('getDebugEl: No active component fixture has been bootstrapped.');
  }
  if (token) {
    const predicate = typeof token === 'function' ? By.directive(token) : token;
    return currentFixture.debugElement.query(predicate);
  }
  return currentFixture.debugElement;
}

export function getEls(selector: string): HTMLElement[] {
  if (!currentFixture) {
    throw new Error('getEls: No active component fixture has been bootstrapped.');
  }
  return Array.from(currentFixture.nativeElement.querySelectorAll(selector));
}

export function hasEl(selector: string): boolean {
  if (!currentFixture) {
    return false;
  }
  return !!currentFixture.nativeElement.querySelector(selector);
}

// 8. Define flush async helper
export async function flush() {
  if (currentFixture) {
    currentFixture.detectChanges();
    await currentFixture.whenStable();
    currentFixture.detectChanges();
  }
}

// 9. Define extra Catalyst helpers
export async function trigger(el: HTMLElement, eventName: string, eventData?: any) {
  let event: Event;
  if (eventName === 'click' || eventName === 'mousedown' || eventName === 'mouseup') {
    event = new MouseEvent(eventName, { bubbles: true, cancelable: true, ...eventData });
  } else if (eventName.startsWith('key')) {
    event = new KeyboardEvent(eventName, { bubbles: true, cancelable: true, ...eventData });
  } else {
    event = new CustomEvent(eventName, { bubbles: true, cancelable: true, detail: eventData });
  }
  el.dispatchEvent(event);
  await flush();
}

export function bootstrapTemplate(template: string, options?: any): any {
  @Component({
    template: template,
    standalone: false,
  })
  class DynamicHostComponent {
    constructor() {
      if (options) {
        for (const key of Object.keys(options)) {
          Object.defineProperty(this, key, {
            get: () => options[key],
            set: (val) => {
              options[key] = val;
            },
            configurable: true,
            enumerable: true,
          });
        }
      }
    }
  }

  TestBed.configureTestingModule({
    declarations: [DynamicHostComponent],
  });

  return bootstrap(DynamicHostComponent);
}

export function useAutoTick() {
  beforeEach(() => {
    jasmine.clock().install();
  });
  afterEach(() => {
    jasmine.clock().uninstall();
  });
}

export async function markForCheckAndFlush() {
  if (currentFixture) {
    currentFixture.changeDetectorRef.markForCheck();
    await flush();
  }
}

// 10. Define setupObservableMatchers and async matchers
export function setupObservableMatchers() {
  jasmine.addAsyncMatchers({
    toEmit(util: any, customEqualityTesters: any): any {
      return {
        compare(actual: any, expected: any): any {
          return new Promise<any>(async (resolve) => {
            let emitted = false;
            let emittedValue: any = undefined;

            const sub = actual.subscribe({
              next: (val: any) => {
                emitted = true;
                emittedValue = val;
              },
            });

            await Promise.resolve();
            sub.unsubscribe();

            if (!emitted) {
              resolve({
                pass: false,
                message: `Expected Observable to emit ${JSON.stringify(expected)} but it did not emit any value.`,
              });
              return;
            }

            const pass = util.equals(emittedValue, expected, customEqualityTesters);
            resolve({
              pass,
              message: pass
                ? `Expected Observable not to emit ${JSON.stringify(expected)}`
                : `Expected Observable to emit ${JSON.stringify(expected)} but it emitted ${JSON.stringify(emittedValue)}`,
            });
          });
        },
      };
    },
    toEmitSequence(util: any, customEqualityTesters: any): any {
      return {
        compare(actual: any, expectedSequence: any[]): any {
          return new Promise<any>(async (resolve) => {
            const emittedValues: any[] = [];
            const sub = actual.subscribe({
              next: (val: any) => {
                emittedValues.push(val);
              },
            });

            await Promise.resolve();
            sub.unsubscribe();

            const pass = util.equals(emittedValues, expectedSequence, customEqualityTesters);
            resolve({
              pass,
              message: pass
                ? `Expected Observable not to emit sequence ${JSON.stringify(expectedSequence)}`
                : `Expected Observable to emit sequence ${JSON.stringify(expectedSequence)} but it emitted ${JSON.stringify(emittedValues)}`,
            });
          });
        },
      };
    },
    toHaveNeverEmitted(util: any, customEqualityTesters: any): any {
      return {
        compare(actual: any): any {
          return new Promise<any>(async (resolve) => {
            let emitted = false;
            let emittedValue: any = undefined;

            const sub = actual.subscribe({
              next: (val: any) => {
                emitted = true;
                emittedValue = val;
              },
            });

            await Promise.resolve();
            sub.unsubscribe();

            resolve({
              pass: !emitted,
              message: !emitted
                ? `Expected Observable to have emitted a value but it did not.`
                : `Expected Observable not to have emitted any values but it emitted ${JSON.stringify(emittedValue)}`,
            });
          });
        },
      };
    },
  } as any);
}
