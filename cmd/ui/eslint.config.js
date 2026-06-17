import tseslint from 'typescript-eslint';
import simpleImportSort from 'eslint-plugin-simple-import-sort';

import eslintConfigPrettier from 'eslint-config-prettier';

export default tseslint.config(
  {
    files: ['**/*.ts'],
    languageOptions: {
      parser: tseslint.parser,
    },
    plugins: {
      'simple-import-sort': simpleImportSort,
    },
    rules: {
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
            ['^\\.']
          ]
        }
      ],
      'simple-import-sort/exports': 'error',
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            {
              group: ['**/../../**'],
              message: 'Relative imports must not go up more than one level. Use absolute paths (xmanager_cloud/...) instead.',
            },
          ],
        },
      ],
      'max-lines-per-function': ['warn', { max: 35, skipComments: true }],
      'complexity': ['warn', { max: 5, variant: 'modified' }],
    },
  },
  {
    files: [
      '**/*_test.ts',
      '**/*.d.ts',
      '**/testing/**',
      '**/stubs/**',
      '**/mock_*.ts',
    ],
    rules: {
      'max-lines-per-function': 'off',
      'complexity': 'off',
    },
  },
  {
    files: [
      '**/common/artifacts/artifact_utils.ts',
      '**/common/components/resource_chip/utils/resource_set_to_resource_chips.ts',
      '**/common/components/status_messages_list/status_messages_list_filter/status_messages_list_filter.ts',
      '**/safevalues_helper.ts',
      '**/service/experiment_service.ts',
      '**/service/work_units_service.ts',
    ],
    rules: {
      'max-lines-per-function': 'off',
      'complexity': 'off',
    },
  },
  eslintConfigPrettier
);
