import { expect, test } from '@playwright/test';
import { CITY_OWN } from '../src/test/me';
import {
  check,
  collectErrors,
  HEIGHT,
  LINUX,
  SCREENS_PORT,
  SHOT,
  SKIP_REASON,
  THEMES,
  WIDTHS,
} from './params';

// Ключевые экраны (спека UI-фундамента §5.3, план UF3.6): сборка режима shots (dist-shots — моки MSW в браузере на
// фикстурах тестов), три ширины × обе темы, снимок окна во всю страницу. Главная добавится с UF4.
const SCREENS = [
  { name: 'login', path: '/login', role: 'anonymous' },
  { name: 'audit', path: '/audit', role: 'admin' },
  { name: 'staff', path: '/staff', role: 'owner' },
  { name: 'cities', path: '/geo/cities', role: 'admin' },
  { name: 'city', path: `/geo/cities/${CITY_OWN}`, role: 'admin' },
] as const;
const base = `http://127.0.0.1:${SCREENS_PORT}`;

test.skip(!LINUX, SKIP_REASON);

for (const sc of SCREENS) {
  for (const theme of THEMES) {
    for (const width of WIDTHS) {
      test(`${sc.name} ${theme} ${width}`, async ({ page }, info) => {
        const errors = collectErrors(page);
        await page.setViewportSize({ width, height: HEIGHT[width] ?? 900 });
        // тема — выбор сотрудника (wf.theme): его читает public/theme-init.js до первого кадра
        await page.addInitScript((t) => localStorage.setItem('wf.theme', t), theme);
        await page.goto(`${base}${sc.path}?shots-role=${sc.role}`);
        await page
          .locator('html[data-shots-ready]')
          .waitFor({ state: 'attached', timeout: 60_000 });
        const unhandled = await page.evaluate(
          () => (window as { __wfShots?: { unhandled: number } }).__wfShots?.unhandled ?? -1,
        );
        expect(unhandled, 'неописанных запросов к API').toBe(0);
        expect(errors).toEqual([]);
        check(
          info,
          `screens/${sc.name}-${theme}-${width}.png`,
          await page.screenshot({ ...SHOT, fullPage: true }),
        );
      });
    }
  }
}
