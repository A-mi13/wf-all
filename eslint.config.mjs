// Линт общих пакетов: границы монорепо (пакеты не импортируют приложения).
// У приложений свои eslint.config.mjs — ESLint берёт ближайший к cwd, этот их не касается.
import { fileURLToPath } from 'node:url';
import { boundariesConfig } from '@wf/config/eslint';
import { defineConfig, globalIgnores } from 'eslint/config';
import tseslint from 'typescript-eslint';

const repoRoot = fileURLToPath(new URL('.', import.meta.url));

export default defineConfig([
  // фикстуры стража границ нарушают их намеренно
  globalIgnores(['packages/config/test/fixtures/**']),
  {
    files: ['packages/**/*.{js,mjs,ts,tsx}'],
    languageOptions: { parser: tseslint.parser },
    extends: [boundariesConfig(repoRoot)],
  },
]);
