// Границы монорепо: приложения не импортируют друг друга, общее — только пакеты @wf/*.
import boundaries from 'eslint-plugin-boundaries';

/** @param {string} rootPath корень репозитория (или фикстур в тестах) */
export function boundariesConfig(rootPath) {
  return {
    plugins: { boundaries },
    settings: {
      'boundaries/root-path': rootPath,
      'boundaries/elements': [
        { type: 'web', pattern: 'apps/web' },
        { type: 'admin', pattern: 'apps/admin' },
        { type: 'package', pattern: 'packages/*' },
      ],
      'import/resolver': { typescript: { alwaysTryTypes: true } },
    },
    rules: {
      'boundaries/dependencies': [
        'error',
        {
          default: 'allow',
          policies: [
            { from: { element: { type: 'web' } }, disallow: { to: { element: { type: 'admin' } } } },
            { from: { element: { type: 'admin' } }, disallow: { to: { element: { type: 'web' } } } },
            { from: { element: { type: 'package' } }, disallow: { to: { element: { type: ['web', 'admin'] } } } },
          ],
        },
      ],
    },
  };
}
