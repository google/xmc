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

import { Type } from '@angular/core';
import { TestModuleMetadata } from '@angular/core/testing';
import { ComponentHarness, HarnessQuery } from '@angular/cdk/testing';

declare global {
  const require: {
    context(path: string, deep?: boolean, filter?: RegExp): {
      keys(): string[];
      <T>(id: string): T;
    };
  };
  function cleanState(factory: () => any, beforeEachFn: typeof beforeEach): any;
  function setupModule(moduleDef: TestModuleMetadata): void;
  function bootstrap(component: any, options?: any): any;
  function get(token: any): any;
  function getHarness(query: any): Promise<any>;
  function getEl<T extends HTMLElement = HTMLElement>(selector: string): T;
  function getEls(selector: string): HTMLElement[];
  function getDebugEl(token?: any): any;
  function hasEl(selector: string): boolean;
  function flush(): Promise<void>;
  function setupObservableMatchers(): void;
  function trigger(el: HTMLElement, eventName: string, eventData?: any): Promise<void>;
  function bootstrapTemplate(template: string, options?: any): any;
  function markForCheckAndFlush(): Promise<void>;
  const pit: any;

  function useAutoTick(): void;

  namespace jasmine {
    interface AsyncMatchers<T, U> {
      toEmit(expectedValue: any): Promise<void>;
      toEmitSequence(expectedSequence: any[]): Promise<void>;
      toHaveNeverEmitted(): Promise<void>;
    }
  }
}
