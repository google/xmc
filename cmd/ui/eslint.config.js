/**
 * Copyright 2026 Google LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import tseslint from 'typescript-eslint';
import simpleImportSort from 'eslint-plugin-simple-import-sort';
import angular from '@angular-eslint/eslint-plugin';

import eslintConfigPrettier from 'eslint-config-prettier';

export default tseslint.config(
  {
    files: ['**/*.ts'],
    extends: [...tseslint.configs.recommended],
    languageOptions: {
      parser: tseslint.parser,
    },
    plugins: {
      'simple-import-sort': simpleImportSort,
      '@angular-eslint': angular,
    },
    rules: {
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
        },
      ],
      'simple-import-sort/imports': [
        'error',
        {
          groups: [
            // Side effect imports (e.g. polyfills, CSS/SCSS).
            ['^\\u0000'],
            // Third-party packages (e.g. @angular, rxjs, jasmine).
            ['^@?\\w'],
            // First-party absolute imports (starts with xmanager_cloud/).
            ['^xmanager_cloud/'],
            // Other absolute imports (e.g. stubs or un-grouped libraries).
            ['^'],
            // Relative imports (starts with a dot).
            ['^\\.'],
          ],
        },
      ],
      'simple-import-sort/exports': 'error',
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            {
              group: ['**/../../**'],
              message:
                'Relative imports must not go up more than one level. Use absolute paths (xmanager_cloud/...) instead.',
            },
          ],
        },
      ],
      'max-lines-per-function': ['warn', { max: 35, skipComments: true }],
      complexity: ['warn', { max: 10, variant: 'modified' }],
      'max-depth': ['error', 3],

      '@angular-eslint/directive-selector': 'off',
      '@angular-eslint/component-selector': 'off',
      '@angular-eslint/no-output-on-prefix': 'error',
      '@angular-eslint/no-input-rename': 'error',
      '@angular-eslint/no-output-rename': 'error',
      '@angular-eslint/use-lifecycle-interface': 'error',
      '@angular-eslint/use-pipe-transform-interface': 'error',
      '@angular-eslint/prefer-standalone': 'error',
    },
  },
  {
    files: ['**/*_test.ts', '**/*.d.ts', '**/testing/**', '**/stubs/**', '**/mock_*.ts'],
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-unused-vars': 'off',
      '@angular-eslint/prefer-standalone': 'off',
      'max-lines-per-function': 'off',
      complexity: 'off',
    },
  },
  {
    files: ['**/*.ts'],
    ignores: [
      '**/*_test.ts',
      '**/*.d.ts',
      '**/testing/**',
      '**/stubs/**',
      '**/mock_*.ts',
      '**/app_config.ts',
    ],
    rules: {
      'no-restricted-syntax': [
        'error',
        {
          selector:
            'Identifier[name=/^(Mock[A-Z]|mock[A-Z]|mock$)/]:not(MemberExpression > .property):not(ObjectExpression > Property > .key):not(MethodDefinition > .key):not(PropertyDefinition > .key):not(TSPropertySignature > .key):not(TSMethodSignature > .key)',
          message:
            'Identifiers starting with "Mock" or "mock" are not allowed in production files. Move mock classes and data to dedicated testing/mock files, and only reference them in app_config.ts for DI provisioning.',
        },
      ],
    },
  },
  eslintConfigPrettier,
);
