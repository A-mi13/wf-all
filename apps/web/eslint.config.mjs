// next lint в Next 16 удалён — ESLint вызывается напрямую.
import { fileURLToPath } from 'node:url';
import { boundariesConfig } from '@wf/config/eslint';
import nextVitals from 'eslint-config-next/core-web-vitals';
import nextTs from 'eslint-config-next/typescript';
import { defineConfig, globalIgnores } from 'eslint/config';

const repoRoot = fileURLToPath(new URL('../..', import.meta.url));

export default defineConfig([
  ...nextVitals,
  ...nextTs,
  boundariesConfig(repoRoot),
  globalIgnores(['.next/**', 'out/**', 'next-env.d.ts', 'src/api/gen/**', 'playwright-report/**']),
]);
