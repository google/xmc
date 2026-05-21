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
import 'jasmine';

import {provideRouter} from '@angular/router';

import {Tag} from './tag';

describe('Tag Component', () => {
  cleanState(() => {
    setupModule({
      imports: [Tag],
      providers: [provideRouter([])],
    });
    return {};
  }, beforeEach);

  it('should be created', () => {
    const comp = bootstrap(Tag, {text: 'test'});
    expect(comp).toBeTruthy();
  });

  it('should remove leading/trailing spaces', () => {
    const comp = bootstrap(Tag, {text: ' test '});
    expect(comp.encodedTag()).toEqual('test');
  });

  it('should escape backslashes', () => {
    const comp = bootstrap(Tag, {text: 'te\\st'});
    expect(comp.encodedTag()).toEqual('te\\\\st');
  });

  it('should escape double quotes', () => {
    const comp = bootstrap(Tag, {text: 'te"st'});
    expect(comp.encodedTag()).toEqual('te\\"st');
  });

  it('should escape backslashes and double quotes', () => {
    const comp = bootstrap(Tag, {text: 'te\\"st'});
    expect(comp.encodedTag()).toEqual('te\\\\\\"st');
  });

  it('should handle empty tag after trim', () => {
    const comp = bootstrap(Tag, {text: ' '});
    expect(comp.encodedTag()).toEqual('');
  });

  it('should have a link by default', () => {
    bootstrap(Tag, {text: 'test'});
    expect(hasEl('a')).toBeTrue();
  });

  it('should disable links when disableLinks is true', () => {
    bootstrap(Tag, {text: 'test', disableLinks: true});
    expect(hasEl('a')).toBeFalse();
  });

});