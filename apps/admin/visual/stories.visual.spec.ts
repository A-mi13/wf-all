import { existsSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from '@playwright/test';
import {
  check,
  collectErrors,
  planShot,
  HEIGHT,
  LINUX,
  SHOT,
  SKIP_REASON,
  STORIES_PORT,
  THEMES,
  WIDTHS,
} from './params';

// Истории витрины (спека UI-фундамента §5.2–§5.3, план UF3.6): список — dist-stories/stories.json (ladle build +
// .ladle/stories-json.mjs). visual — снимок в обеих темах (layout — на трёх ширинах, прочие — 1440); остальные —
// дымовой прогон без картинки: история отрисовалась, ошибок нет. Снимок — корень истории ([data-showcase-root]);
// у оверлеев и тостов (портал в body) — окно.

type Entry = { id: string; title: string; visual: boolean; layout: boolean };
const LIST = join(import.meta.dirname, '../dist-stories/stories.json');
const stories: Entry[] = existsSync(LIST) ? JSON.parse(readFileSync(LIST, 'utf8')) : [];
const base = `http://127.0.0.1:${STORIES_PORT}`;

test.skip(!LINUX, SKIP_REASON);

test('список историй собран (ladle build + stories-json)', () => {
  expect(stories.length, `${LIST}: нет историй — сначала stories:build`).toBeGreaterThan(0);
});

for (const s of stories) {
  const plan = s.visual
    ? THEMES.flatMap((theme) => (s.layout ? WIDTHS : [1440]).map((width) => ({ theme, width })))
    : [{ theme: 'light' as const, width: 1440 }];
  for (const { theme, width } of plan) {
    test(`${s.id} ${theme} ${width}`, async ({ page }, info) => {
      // снимок — только у ключевых (visual); остальные — дымовая проверка без снимка
      const shot = s.visual ? planShot(info, `stories/${s.id}-${theme}-${width}.png`) : '';
      const errors = collectErrors(page);
      await page.setViewportSize({ width, height: HEIGHT[width] ?? 900 });
      await page.goto(`${base}/?story=${s.id}&mode=preview&theme=${theme}`);
      // корень есть до монтажа истории (ShowcaseProvider ждёт записи темы) — ждём содержимое, диалог или тост
      await page.waitForFunction(() => {
        const root = document.querySelector('[data-showcase-root]');
        return (
          root !== null &&
          (root.childElementCount > 0 ||
            document.querySelector('[role="dialog"]') !== null ||
            document.querySelector('[data-wf-toasts] [aria-live] > *') !== null)
        );
      });
      await page.evaluate(() => document.fonts.ready);
      expect(errors).toEqual([]);
      if (!s.visual) return;
      const portal = await page
        .locator('[role="dialog"], [data-wf-toasts] [aria-live] > *')
        .count();
      const png = portal
        ? await page.screenshot(SHOT)
        : await page.locator('[data-showcase-root]').screenshot(SHOT);
      check(info, shot, png);
    });
  }
}
