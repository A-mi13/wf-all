import { fileURLToPath } from 'node:url';
import { boundariesConfig } from '@wf/config/eslint';
import { defineConfig, globalIgnores } from 'eslint/config';
import reactHooks from 'eslint-plugin-react-hooks';
import { reactRefresh } from 'eslint-plugin-react-refresh';
import tseslint from 'typescript-eslint';

const repoRoot = fileURLToPath(new URL('../..', import.meta.url));

export default defineConfig([
  globalIgnores([
    'dist/**',
    'dist-stories/**',
    'dist-shots/**',
    'src/api/gen/**',
    'playwright-report/**',
    'test-results/**',
    'visual/report/**',
    'visual/test-results/**',
    'visual/.out/**',
    'visual/.baseline/**',
  ]),
  tseslint.configs.recommended,
  reactHooks.configs.flat.recommended,
  reactRefresh.configs.vite(),
  boundariesConfig(repoRoot),
  // Числа, даты, деньги в экранах — только хуками src/i18n и src/ui/time (useNumber, useMoney, useDateTime,
  // RelativeTime): язык и пояс интерфейса, деньги без float (спека UI-фундамента §4.7, план UF2a.9).
  {
    files: ['src/features/**/*.{ts,tsx}'],
    rules: {
      // Intl — любая ссылка на глобальное имя: new, вызов, псевдоним, деструктуризация (ревью UF2a/II-B, m4)
      'no-restricted-globals': [
        'error',
        {
          name: 'Intl',
          message:
            'Числа, даты, деньги в экране — useNumber / useMoney / useDateTime / RelativeTime (src/i18n, src/ui/time).',
        },
      ],
      // toLocale* — и вызов, и обращение (`n['toLocaleString']`, `Number.prototype.toLocaleString.call`,
      // деструктуризация); globalThis.Intl / window.Intl / self.Intl
      'no-restricted-properties': [
        'error',
        ...['toLocaleString', 'toLocaleDateString', 'toLocaleTimeString'].map((property) => ({
          property,
          message:
            'Числа, даты, деньги в экране — useNumber / useMoney / useDateTime / RelativeTime (src/i18n, src/ui/time).',
        })),
        ...['globalThis', 'window', 'self'].map((object) => ({
          object,
          property: 'Intl',
          message:
            'Числа, даты, деньги в экране — useNumber / useMoney / useDateTime / RelativeTime (src/i18n, src/ui/time).',
        })),
      ],
    },
  },
]);
