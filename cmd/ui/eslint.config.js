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
import unusedImports from 'eslint-plugin-unused-imports';
import angular from '@angular-eslint/eslint-plugin';
import angularTemplate from '@angular-eslint/eslint-plugin-template';
import angularTemplateParser from '@angular-eslint/template-parser';

import eslintConfigPrettier from 'eslint-config-prettier';

export default tseslint.config(
  {
    ignores: [
      '**/dist/**',
      '**/node_modules/**',
      '**/coverage/**',
      '**/out-tsc/**',
      '**/*.scss',
      '**/*.css',
    ],
  },
  {
    files: ['**/*.ts'],
    extends: [...tseslint.configs.recommended],
    languageOptions: {
      parser: tseslint.parser,
    },
    plugins: {
      'simple-import-sort': simpleImportSort,
      'unused-imports': unusedImports,
      '@angular-eslint': angular,
    },
    rules: {
      // General JavaScript / TypeScript Quality & Immutability
      eqeqeq: ['error', 'always', { null: 'ignore' }],
      'prefer-const': ['error', { destructuring: 'all' }],
      'no-var': 'error',
      'no-console': ['error', { allow: ['warn', 'error', 'info'] }],

      'unused-imports/no-unused-imports': 'error',
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
        },
      ],
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/no-duplicate-enum-values': 'error',
      '@typescript-eslint/no-non-null-asserted-optional-chain': 'error',
      '@typescript-eslint/no-unnecessary-type-constraint': 'error',
      '@typescript-eslint/prefer-as-const': 'error',
      '@typescript-eslint/prefer-for-of': 'error',
      '@typescript-eslint/no-inferrable-types': [
        'error',
        { ignoreParameters: true, ignoreProperties: true },
      ],
      '@typescript-eslint/no-empty-object-type': 'error',
      '@typescript-eslint/no-wrapper-object-types': 'error',

      'simple-import-sort/imports': [
        'error',
        {
          groups: [
            // Side effect imports (e.g. polyfills, CSS/SCSS).
            ['^\\u0000'],
            // Third-party packages (e.g. @angular, rxjs, jasmine).
            ['^@?\\w'],
            // First-party absolute imports (starts with app/).
            ['^app/'],
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
                'Relative imports must not go up more than one level. Use absolute paths (app/...) instead.',
            },
            {
              group: ['xmanager_cloud/**', 'longrunning/**'],
              message:
                'Legacy google3 import paths (xmanager_cloud/..., longrunning/...) are deprecated. Use app/... or app/protos/... instead.',
            },
          ],
        },
      ],
      'max-lines': ['error', { max: 300, skipBlankLines: true, skipComments: true }],
      'max-lines-per-function': ['error', { max: 35, skipComments: true }],
      complexity: ['error', { max: 10, variant: 'modified' }],
      'max-depth': ['error', 3],

      // Angular Component & Lifecycle Rules
      '@angular-eslint/directive-selector': 'off',
      '@angular-eslint/component-selector': 'off',
      '@angular-eslint/no-output-on-prefix': 'error',
      '@angular-eslint/no-input-rename': 'error',
      '@angular-eslint/no-output-rename': 'error',
      '@angular-eslint/no-inputs-metadata-property': 'error',
      '@angular-eslint/no-outputs-metadata-property': 'error',
      '@angular-eslint/use-lifecycle-interface': 'error',
      '@angular-eslint/use-pipe-transform-interface': 'error',
      '@angular-eslint/prefer-standalone': 'error',
      '@angular-eslint/no-empty-lifecycle-method': 'error',
      '@angular-eslint/contextual-lifecycle': 'error',
      '@angular-eslint/no-pipe-impure': 'error',
      '@angular-eslint/directive-class-suffix': 'error',

      'no-restricted-syntax': [
        'error',
        {
          selector: 'Decorator[expression.callee.name="Input"], Decorator[expression.name="Input"]',
          message: 'Use signal primitives `input()` instead of `@Input`.',
        },
        {
          selector:
            'Decorator[expression.callee.name="Output"], Decorator[expression.name="Output"]',
          message: 'Use signal primitives `output()` instead of `@Output`.',
        },
        {
          selector:
            'Decorator[expression.callee.name="ViewChild"], Decorator[expression.name="ViewChild"]',
          message: 'Use signal primitives `viewChild()` instead of `@ViewChild`.',
        },
      ],
    },
  },
  {
    files: [
      '**/*_test.ts',
      '**/*.spec.ts',
      '**/*.d.ts',
      '**/testing/**',
      '**/mock_*.ts',
      '**/screenshot_tests/**',
    ],
    rules: {
      '@angular-eslint/prefer-standalone': 'off',
      'max-lines': ['error', { max: 600, skipBlankLines: true, skipComments: true }],
      'max-lines-per-function': 'off',
      complexity: 'off',
    },
  },
  {
    files: ['**/app/testing/**', '**/*.d.ts'],
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
    },
  },
  {
    files: ['**/*.d.ts'],
    rules: {
      '@typescript-eslint/no-unused-vars': 'off',
    },
  },
  {
    files: ['**/*.ts'],
    ignores: [
      '**/*_test.ts',
      '**/*.spec.ts',
      '**/*.d.ts',
      '**/testing/**',
      '**/mock_*.ts',
      '**/screenshot_tests/**',
      '**/app_config.ts',
    ],
    rules: {
      'no-restricted-syntax': [
        'error',
        {
          selector: 'Decorator[expression.callee.name="Input"], Decorator[expression.name="Input"]',
          message: 'Use signal primitives `input()` instead of `@Input`.',
        },
        {
          selector:
            'Decorator[expression.callee.name="Output"], Decorator[expression.name="Output"]',
          message: 'Use signal primitives `output()` instead of `@Output`.',
        },
        {
          selector:
            'Decorator[expression.callee.name="ViewChild"], Decorator[expression.name="ViewChild"]',
          message: 'Use signal primitives `viewChild()` instead of `@ViewChild`.',
        },
        {
          selector:
            'Identifier[name=/^(Mock[A-Z]|mock[A-Z]|mock$)/]:not(MemberExpression > .property):not(ObjectExpression > Property > .key):not(MethodDefinition > .key):not(PropertyDefinition > .key):not(TSPropertySignature > .key):not(TSMethodSignature > .key)',
          message:
            'Identifiers starting with "Mock" or "mock" are not allowed in production files. Move mock classes and data to dedicated testing/mock files, and only reference them in app_config.ts for DI provisioning.',
        },
      ],
    },
  },
  {
    files: ['**/*.html'],
    ignores: ['**/index.html'],
    languageOptions: {
      parser: angularTemplateParser,
    },
    plugins: {
      '@angular-eslint/template': angularTemplate,
    },
    rules: {
      '@angular-eslint/template/prefer-control-flow': 'error',
      '@angular-eslint/template/banana-in-box': 'error',
      '@angular-eslint/template/button-has-type': 'error',
      '@angular-eslint/template/no-negated-async': 'error',
      '@angular-eslint/template/attributes-order': 'error',
      '@angular-eslint/template/prefer-self-closing-tags': 'error',
      '@angular-eslint/template/no-duplicate-attributes': 'error',
      '@angular-eslint/template/eqeqeq': 'error',
      '@angular-eslint/template/prefer-at-empty': 'error',
      '@angular-eslint/template/no-empty-control-flow': 'error',
      '@angular-eslint/template/alt-text': 'error',
      '@angular-eslint/template/valid-aria': 'error',
      '@angular-eslint/template/elements-content': 'error',
      '@angular-eslint/template/no-positive-tabindex': 'error',
      'max-lines': ['error', { max: 300, skipBlankLines: true, skipComments: true }],
      '@angular-eslint/template/cyclomatic-complexity': ['error', { maxComplexity: 10 }],
      '@angular-eslint/template/conditional-complexity': ['error', { maxComplexity: 4 }],
      '@angular-eslint/template/no-inline-styles': ['error', { allowBindToStyle: true }],
      '@angular-eslint/template/no-any': 'error',
      '@angular-eslint/template/prefer-at-else': 'error',
      '@angular-eslint/template/no-interpolation-in-attributes': 'error',
    },
  },
  eslintConfigPrettier,
);
