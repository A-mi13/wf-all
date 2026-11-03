import assert from 'node:assert/strict';
import { join } from 'node:path';
import { test } from 'node:test';
import { consoleProblem, parseArgs, planShots, shotUrl, slug } from './admin-shots.mjs';

// Снимки для ревьюера (спека §5.4, план UF1a.1): разбор аргументов и раскладка файлов. Браузер и Vite
// здесь не запускаются — только чистые функции скрипта.

test('без --out — ошибка с подсказкой', () => {
  assert.throws(() => parseArgs(['/audit']), /--out <каталог>/);
});

test('без путей — ошибка', () => {
  assert.throws(() => parseArgs(['--out', 'x']), /путь/);
});

test('умолчания: роль admin, три ширины, обе темы, русский', () => {
  const o = parseArgs(['--out', 'shots', '/audit', '/staff']);
  assert.deepEqual(o, {
    out: 'shots',
    role: 'admin',
    widths: [1440, 768, 390],
    themes: ['dark', 'light'],
    locale: 'ru',
    paths: ['/audit', '/staff'],
  });
});

test('флаги в любом месте и через =', () => {
  const o = parseArgs([
    '/me',
    '--role=moderator',
    '--widths',
    '390',
    '--themes=light',
    '--locale',
    'en',
    '--out=s',
  ]);
  assert.equal(o.role, 'moderator');
  assert.deepEqual(o.widths, [390]);
  assert.deepEqual(o.themes, ['light']);
  assert.equal(o.locale, 'en');
  assert.deepEqual(o.paths, ['/me']);
});

test('неизвестные роль, тема, язык, ширина и флаг — ошибка', () => {
  assert.throws(() => parseArgs(['--out', 's', '--role', 'root', '/a']), /роль root/);
  assert.throws(() => parseArgs(['--out', 's', '--themes', 'dark,sepia', '/a']), /тема sepia/);
  assert.throws(() => parseArgs(['--out', 's', '--locale', 'de', '/a']), /язык de/);
  assert.throws(() => parseArgs(['--out', 's', '--widths', '1440,abc', '/a']), /ширина abc/);
  assert.throws(() => parseArgs(['--out', 's', '--widths', '0', '/a']), /ширина 0/);
  assert.throws(() => parseArgs(['--out', 's', '--force', '/a']), /--force/);
});

// Git Bash (MSYS) переписывает аргумент «/audit» в «C:/Program Files/Git/audit» при запуске task.exe —
// такой путь не адрес админки: ошибка с подсказкой, а не снимок страницы «не найдено».
test('путь без ведущего / — от корня; путь Windows (MSYS переписал) — ошибка с подсказкой', () => {
  assert.deepEqual(parseArgs(['--out', 's', 'audit', 'geo/cities?q=a']).paths, [
    '/audit',
    '/geo/cities?q=a',
  ]);
  assert.throws(
    () => parseArgs(['--out', 's', 'C:/Program Files/Git/audit']),
    /MSYS_NO_PATHCONV=1/,
  );
  assert.throws(() => parseArgs(['--out', 's', 'C:\\x']), /MSYS_NO_PATHCONV=1/);
});

test('путь → безопасное имя файла', () => {
  assert.equal(slug('/'), 'root');
  assert.equal(slug('/audit'), 'audit');
  assert.equal(slug('/geo/cities?status=["live"]&q=%D0%9C'), 'geo-cities-status-live-q-D0-9C');
  assert.equal(slug('/geo/cities/3f2c8e1a-9b4d'), 'geo-cities-3f2c8e1a-9b4d');
  assert.equal(slug('/geo/cities?q=Москва…'), 'geo-cities-q');
  for (const s of [slug('/a?b=c/d%20e'), slug('/x\\y:z*?"<>|')]) {
    assert.match(s, /^[A-Za-z0-9_-]+$/);
  }
});

test('список снимков = пути × ширины × темы; en — суффикс', () => {
  const o = parseArgs(['--out', 'shots', '--widths', '1440,390', '/audit', '/geo/cities?status=x']);
  const plan = planShots(o);
  assert.equal(plan.length, 2 * 2 * 2);
  assert.deepEqual(
    plan.map((s) => s.file),
    [
      'audit-1440-dark.png',
      'audit-1440-light.png',
      'audit-390-dark.png',
      'audit-390-light.png',
      'geo-cities-status-x-1440-dark.png',
      'geo-cities-status-x-1440-light.png',
      'geo-cities-status-x-390-dark.png',
      'geo-cities-status-x-390-light.png',
    ].map((f) => join('shots', f)),
  );
  const en = planShots(
    parseArgs(['--out', 'o', '--locale', 'en', '--widths', '768', '--themes', 'dark', '/me']),
  );
  assert.deepEqual(
    en.map((s) => s.file),
    [join('o', 'me-768-dark-en.png')],
  );
});

test('два пути с одним именем файла — ошибка, а не перезапись', () => {
  assert.throws(() => planShots(parseArgs(['--out', 'o', '/a/b', '/a-b'])), /одно имя/);
});

test('адрес снимка: роль и язык — параметрами, свой query пути сохраняется', () => {
  assert.equal(
    shotUrl('http://127.0.0.1:5176', '/audit', 'admin', 'ru'),
    'http://127.0.0.1:5176/audit?shots-role=admin',
  );
  assert.equal(
    shotUrl('http://127.0.0.1:5176', '/geo/cities?q=a', 'owner', 'en'),
    'http://127.0.0.1:5176/geo/cities?q=a&shots-role=owner&shots-locale=en',
  );
});

// Ответ мока не-2xx (401 у anonymous, 403 «нет прав», 404 «не найден») Chromium сам пишет в консоль как
// error «Failed to load resource» — это штатный ответ, не проблема снимка (раунд 1 ревью, I-2).
// Неописанный запрос ловят console.error MSW и счётчик __wfShots.unhandled.
test('ошибки консоли: «Failed to load resource» для /v1/* того же origin — не проблема', () => {
  const base = 'http://127.0.0.1:5176';
  const failed =
    'Failed to load resource: the server responded with a status of 401 (Unauthorized)';
  assert.equal(consoleProblem('error', failed, `${base}/v1/staff/me`, base), null);
  assert.equal(consoleProblem('error', failed, `${base}/v1/geo/cities/x?lang=ru`, base), null);
  // модуль, шрифт или чужой origin — проблема
  assert.match(
    consoleProblem('error', failed, `${base}/src/x.tsx`, base),
    /Failed to load resource/,
  );
  assert.match(consoleProblem('error', failed, 'http://evil.test/v1/staff/me', base), /Failed/);
  assert.match(consoleProblem('error', failed, `${base}/v1x/staff`, base), /Failed/);
  assert.match(consoleProblem('error', failed, '', base), /Failed/);
  // неописанный запрос MSW и исключения — проблема
  const msw = 'MSW: неописанный запрос — GET http://127.0.0.1:5176/v1/audit';
  assert.match(
    consoleProblem('error', msw, `${base}/src/shots/server.ts`, base),
    /неописанный запрос/,
  );
  assert.match(consoleProblem('error', 'Uncaught Error', `${base}/v1/staff/me`, base), /Uncaught/);
  // не ошибки — не проблема
  assert.equal(consoleProblem('warning', failed, `${base}/src/x.tsx`, base), null);
  assert.equal(consoleProblem('log', 'hello', '', base), null);
});
