import { join } from 'node:path';
import { defineConfig } from '@playwright/test';
import { LINUX, SCREENS_PORT, STORIES_PORT } from './params';

// Визуальные тесты админки (спека UI-фундамента §5.3, план UF3.6) — только Linux-CI, workflow admin-visual.yml
// в образе mcr.microsoft.com/playwright: шрифты Windows дали бы ложные отличия (тесты сами пропускаются не на
// Linux). Перед прогоном — сборки: `ladle build` + `.ladle/stories-json.mjs` (dist-stories) и
// `vite build --mode shots` (dist-shots).
// Эталон — visual/.baseline (артефакт прогона main), снимки этого прогона — visual/.out (следующий эталон на main);
// сравнение — toMatchSnapshot Playwright (без новой зависимости, U17), допуск maxDiffPixelRatio 0.001.
const admin = join(import.meta.dirname, '..');

export default defineConfig({
  testDir: '.',
  testMatch: /\.visual\.spec\.ts$/,
  outputDir: 'test-results',
  snapshotPathTemplate: '{testDir}/.baseline/{arg}{ext}',
  updateSnapshots: 'none',
  fullyParallel: true,
  forbidOnly: true,
  retries: 0,
  reporter: [
    ['list'],
    ['json', { outputFile: 'results.json' }],
    ['html', { outputFolder: 'report', open: 'never' }],
  ],
  expect: { toMatchSnapshot: { maxDiffPixelRatio: 0.001 } },
  use: {
    browserName: 'chromium',
    locale: 'ru-RU',
    timezoneId: 'UTC',
    contextOptions: { reducedMotion: 'reduce' },
  },
  // не на Linux тесты пропускаются (params.ts) — серверы и сборки не нужны
  webServer: !LINUX
    ? undefined
    : [
        {
          command: `node visual/serve.mjs dist-stories ${STORIES_PORT}`,
          cwd: admin,
          url: `http://127.0.0.1:${STORIES_PORT}/`,
          reuseExistingServer: false,
        },
        {
          command: `node visual/serve.mjs dist-shots ${SCREENS_PORT}`,
          cwd: admin,
          url: `http://127.0.0.1:${SCREENS_PORT}/`,
          reuseExistingServer: false,
        },
      ],
});
