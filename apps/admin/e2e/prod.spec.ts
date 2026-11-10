import { readFileSync } from 'node:fs';
import path from 'node:path';
import { type BrowserContext, expect, type Page, test } from '@playwright/test';
import { pairOf, ROOT } from './env';
import { t } from './keys';
import { PROD_SECTIONS } from './sections';
import { acceptAndSetup, bootstrap, newStaff, receiveInvite } from './ui';

// Прод-проект (спека UI-фундамента §4.12, план UF1b.7, U9): собранная админка (vite build из
// scripts/e2e-admin.sh), отданная admin-api со staticCSP, — то, чего dev-сервер не видит:
// (1) тема до первого кадра — public/theme-init.js при явном выборе ПРОТИВ системной темы (I3: умолчание
//     эмуляции совпало бы с ожиданием, и подсадка «без скрипта» осталась бы немой);
// (2) обход всех разделов меню и панели приглашения без единого securitypolicyviolation;
// (3) фон страницы = tokens.color.bg[тема] на собранном CSS для обеих тем (dev ↔ прод, I2).
// Фон — у первого блока #root (каркас и экраны входа красят его bg): у <body> своего фона пока нет
// (глобальный CSS — UF2a.2).

type Theme = 'light' | 'dark';
type Pref = Theme | 'system';

const tokens = JSON.parse(
  readFileSync(path.join(ROOT, 'contracts', 'tokens', 'tokens.json'), 'utf8'),
) as { color: { bg: Record<Theme, string> } };

// #RRGGBB → rgb(r, g, b), как его отдаёт getComputedStyle
function rgb(hex: string): string {
  const n = (i: number) => Number.parseInt(hex.slice(i, i + 2), 16);
  return `rgb(${n(1)}, ${n(3)}, ${n(5)})`;
}
const bg = (theme: Theme) => rgb(tokens.color.bg[theme]);

const ground = (page: Page) =>
  page.evaluate(() => {
    const el = document.querySelector('#root > *');
    return el ? getComputedStyle(el).backgroundColor : '';
  });

// До скриптов документа (addInitScript): выбор темы в storage, атрибут data-theme в момент, когда разбор
// HTML закончен, а модульные скрипты приложения ещё не исполнились (readyState interactive), и нарушения CSP.
type Probe = { firstTheme?: string; csp: string[] };
async function probe(context: BrowserContext, pref?: Pref): Promise<void> {
  await context.addInitScript((pref) => {
    const w = window as unknown as { __wfProbe: Probe };
    w.__wfProbe = { csp: [] };
    if (pref) localStorage.setItem('wf.theme', pref);
    document.addEventListener('readystatechange', () => {
      if (document.readyState === 'interactive')
        w.__wfProbe.firstTheme = document.documentElement.dataset.theme;
    });
    document.addEventListener('securitypolicyviolation', (e) => {
      w.__wfProbe.csp.push(
        `${location.pathname}: ${e.violatedDirective} ${e.blockedURI || ''} ${e.sample || ''}`.trim(),
      );
    });
  }, pref);
}
const probed = (page: Page) =>
  page.evaluate(() => (window as unknown as { __wfProbe: Probe }).__wfProbe);

test.describe.configure({ mode: 'serial' });

test.describe('тема до первого кадра: явный выбор против системной', () => {
  const cases: { pref: Pref; system: Theme; expected: Theme }[] = [
    { pref: 'light', system: 'dark', expected: 'light' },
    { pref: 'dark', system: 'light', expected: 'dark' },
    { pref: 'system', system: 'dark', expected: 'dark' },
    { pref: 'system', system: 'light', expected: 'light' },
  ];
  for (const c of cases) {
    test(`wf.theme=${c.pref} при системной ${c.system} → ${c.expected}`, async ({
      browser,
    }, info) => {
      const context = await browser.newContext({
        baseURL: info.project.use.baseURL,
        colorScheme: c.system,
      });
      await probe(context, c.pref);
      const page = await context.newPage();
      const console: string[] = [];
      page.on('console', (m) => {
        if (/Content Security Policy/i.test(m.text())) console.push(m.text());
      });
      await page.goto('/login');
      await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
      // шрифтовые font-src приходят после загрузки шрифтов (ревью UF1b/II m3): ждём их, и вторая линия —
      // консоль, как в сценарии (2)
      await page.evaluate(() => document.fonts.ready.then(() => undefined));
      const p = await probed(page);
      expect(await ground(page)).toBe(bg(c.expected));
      // атрибут поставил theme-init.js, а не приложение: он уже был до модульных скриптов
      expect(p.firstTheme).toBe(c.pref);
      expect(p.csp).toEqual([]);
      expect(console).toEqual([]);
      await context.close();
    });
  }
});

test('под сотрудником: разделы меню и панель приглашения без нарушений CSP; фон по теме', async ({
  page,
  context,
}, info) => {
  const violations: string[] = [];
  const errors: string[] = [];
  page.on('console', (m) => {
    if (/Content Security Policy/i.test(m.text())) violations.push(`console: ${m.text()}`);
  });
  page.on('pageerror', (e) => errors.push(e.message));
  await probe(context);
  const collect = async () => violations.push(...(await probed(page)).csp);

  const s = newStaff('owner-prod');
  const run = await bootstrap(pairOf(info.project.name), s);
  expect(run.code, run.stderr).toBe(0);
  await receiveInvite(s);
  await acceptAndSetup(page, s);
  await collect();

  // (2) каждый раздел — полной загрузкой по глубокой ссылке: admin-api отдаёт index.html для пути SPA
  for (const section of PROD_SECTIONS) {
    await page.goto(section);
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    expect(new URL(page.url()).pathname, section).toBe(section);
    await collect();
  }
  await page.goto('/staff');
  await page.getByRole('button', { name: t('staff.invite.open') }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await collect();

  // (3) фон собранного CSS — значение токена своей темы; системная — противоположная выбору
  for (const theme of ['light', 'dark'] as const) {
    await page.emulateMedia({ colorScheme: theme === 'light' ? 'dark' : 'light' });
    await page.evaluate((v) => localStorage.setItem('wf.theme', v), theme);
    await page.goto('/audit');
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
    expect(await ground(page), theme).toBe(bg(theme));
    await collect();
  }

  await info.attach('csp-violations', { body: violations.join('\n') || '(нет)' });
  expect(violations).toEqual([]);
  expect(errors).toEqual([]);
});
