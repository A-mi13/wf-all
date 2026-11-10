import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { expect, type Page, type TestInfo } from '@playwright/test';

// Общее визуальных тестов (план UF3.6): порты статики, снимок → visual/.out (следующий эталон) и сравнение с
// visual/.baseline. Режим задаёт workflow (scripts/visual-baseline.mjs mode): VISUAL_COMPARE — эталон скачан,
// VISUAL_INFORMATIVE — отличие пишется в отчёт аннотацией visual-diff, но тест не падает (main, метка
// visual-change, другой образ Playwright у эталона).

// VISUAL_FORCE=1 — прогон не на Linux для отладки самого стенда (снимки с местными шрифтами в эталон не годятся)
export const LINUX = process.platform === 'linux' || process.env.VISUAL_FORCE === '1';
export const SKIP_REASON =
  'визуальные тесты — только в Linux-CI: шрифты Windows дают ложные отличия';
export const STORIES_PORT = 61100;
export const SCREENS_PORT = 61101;
export const THEMES = ['light', 'dark'] as const;
export const WIDTHS = [390, 768, 1440] as const;
export const HEIGHT: Record<number, number> = { 390: 844, 768: 1024, 1440: 900 };

const OUT = join(import.meta.dirname, '.out');
const BASELINE = join(import.meta.dirname, '.baseline');
const compare = process.env.VISUAL_COMPARE === 'true';
const informative = process.env.VISUAL_INFORMATIVE === 'true';

export const SHOT = { animations: 'disabled', caret: 'hide' } as const;

// Снимок name (`stories/<id>-<тема>-<ширина>.png`): в .out всегда; сравнение — если эталон скачан и в нём такой
// снимок есть (нет — новый, аннотация visual-new).
export function check(info: TestInfo, name: string, png: Buffer) {
  const out = join(OUT, name);
  mkdirSync(dirname(out), { recursive: true });
  writeFileSync(out, png);
  if (!compare) return;
  if (!existsSync(join(BASELINE, name))) {
    info.annotations.push({ type: 'visual-new', description: name });
    return;
  }
  try {
    // массив — каталог/файл: строку с «/» Playwright превращает в «-»
    expect(png).toMatchSnapshot(name.split('/'));
  } catch (e) {
    info.annotations.push({ type: 'visual-diff', description: name });
    if (!informative) throw e;
  }
}

// Ошибки страницы — дымовой прогон: история или экран упали либо пишут ошибку в консоль. Ответ мока не-2xx
// (401, 403, 404) Chromium пишет как «Failed to load resource» — для /v1/* того же origin это штатный ответ моков
// (как scripts/admin-shots.mjs consoleProblem).
export function collectErrors(page: Page): string[] {
  const errors: string[] = [];
  page.on('pageerror', (e) => errors.push(e.message));
  page.on('console', (m) => {
    if (m.type() !== 'error') return;
    const url = m.location().url;
    if (m.text().startsWith('Failed to load resource') && url.includes('/v1/')) return;
    errors.push(`${m.text()}${url ? ` (${url})` : ''}`);
  });
  return errors;
}
