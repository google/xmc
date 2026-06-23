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

import 'jasmine';

import { TestBed } from '@angular/core/testing';

import { SplitPipe } from './split_pipe';

describe('SplitPipe', () => {
  let pipe: SplitPipe;

  beforeEach(() => {
    TestBed.configureTestingModule({
      providers: [SplitPipe],
    });
    pipe = TestBed.inject(SplitPipe);
  });

  it('should create an instance', () => {
    expect(pipe).toBeTruthy();
  });

  it('should split string by newline by default', () => {
    const value = 'line1\nline2\nline3';
    expect(pipe.transform(value)).toEqual(['line1', 'line2', 'line3']);
  });

  it('should split string by custom delimiter', () => {
    const value = 'item1,item2,item3';
    expect(pipe.transform(value, ',')).toEqual(['item1', 'item2', 'item3']);
  });

  it('should return empty array for null, undefined, or empty string', () => {
    expect(pipe.transform(null)).toEqual([]);
    expect(pipe.transform(undefined)).toEqual([]);
    expect(pipe.transform('')).toEqual([]);
  });

  it('should return single element array if delimiter is not found', () => {
    const value = 'single_line';
    expect(pipe.transform(value)).toEqual(['single_line']);
  });
});
