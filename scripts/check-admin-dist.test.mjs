// Тесты проверки сборки админки (scripts/check-admin-dist.mjs; план UF1b.4, U4, спека UI-фундамента §2.1).
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { after, before, test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { checkDist, DEFAULT_DIST } from './check-admin-dist.mjs';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const SCRIPT = join(ROOT, 'scripts', 'check-admin-dist.mjs');
let TMP;
before(() => {
  mkdirSync(join(ROOT, '.tools', 'tmp'), { recursive: true });
  TMP = mkdtempSync(join(ROOT, '.tools', 'tmp', 'check-dist-test-'));
});
after(() => rmSync(TMP, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 }));

const CLEAN_CSS =
  ':root{--wf-color-bg:light-dark(#F3F6F3,#0B0F0D)}body{background:var(--wf-color-bg)}';

// Каталог-фикстура «как dist»: { 'assets/x.js': '…' }.
let n = 0;
function dist(files) {
  const dir = join(TMP, `d${n++}`);
  for (const [name, body] of Object.entries(files)) {
    mkdirSync(dirname(join(dir, name)), { recursive: true });
    writeFileSync(join(dir, name), body);
  }
  return dir;
}
const clean = (extra = {}) =>
  dist({
    'index.html': '<!doctype html><script type="module" src="/assets/index.js"></script>',
    'assets/index.js': 'console.log("admin")',
    'assets/index.css': CLEAN_CSS,
    ...extra,
  });

test('чистая сборка — нарушений нет, код 0', () => {
  const dir = clean();
  assert.deepEqual(checkDist(dir), []);
  const r = spawnSync(process.execPath, [SCRIPT, dir], { encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
});

test('полифил --lightningcss- в одном из CSS — ошибка', () => {
  const dir = clean({
    'assets/page.css':
      '.a{--lightningcss-light:initial;--lightningcss-dark: ;color:var(--lightningcss-light,#fff)}',
  });
  const bad = checkDist(dir);
  assert.equal(bad.length, 1);
  assert.match(bad[0], /assets\/page\.css.*--lightningcss-/);
  const r = spawnSync(process.execPath, [SCRIPT, dir], { encoding: 'utf8' });
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /--lightningcss-/);
});

test('нет light-dark( ни в одном CSS — ошибка', () => {
  const dir = clean({ 'assets/index.css': ':root{--wf-color-bg:#0B0F0D}' });
  assert.match(checkDist(dir).join('\n'), /light-dark\(/);
});

test('метки msw в js, html, css и файл воркера — ошибки со списком', () => {
  const dir = clean({
    'assets/a.js': 'navigator.serviceWorker.register("/mockServiceWorker.js")',
    'assets/deep/b.js': 'import("msw/browser").then((m) => m.setupWorker())',
    'index.html': '<!doctype html><script>/* setupWorker */</script>',
    'mockServiceWorker.js': '/* worker */',
  });
  const bad = checkDist(dir).join('\n');
  assert.match(bad, /assets\/a\.js: mockServiceWorker/);
  assert.match(bad, /assets\/deep\/b\.js: msw\/browser/);
  assert.match(bad, /assets\/deep\/b\.js: setupWorker/);
  assert.match(bad, /index\.html: setupWorker/);
  assert.match(bad, /mockServiceWorker\.js: файл воркера MSW/);
});

// План (n2): двоичные шрифты @fontsource не читаются — подстрока msw нашлась бы в них случайно.
test('woff2 с байтами msw и слово msw вне меток — чисто', () => {
  const dir = clean({
    'assets/manrope.woff2': Buffer.from([
      0x77, 0x4f, 0x46, 0x32, 0x6d, 0x73, 0x77, 0x00, 0x6d, 0x73,
    ]),
    'assets/c.js': 'const label = "msw"; // слово, не метка',
  });
  assert.deepEqual(checkDist(dir), []);
});

test('нет каталога или в нём нет CSS/JS — ошибка, а не «чисто»', () => {
  assert.match(checkDist(join(TMP, 'нет-такого')).join('\n'), /нет каталога/);
  assert.match(checkDist(dist({ 'index.html': '<!doctype html>' })).join('\n'), /нет \*\.js/);
  assert.match(
    checkDist(dist({ 'index.html': '<!doctype html>', 'a.js': '' })).join('\n'),
    /нет \*\.css/,
  );
});

// admin:ci идёт из apps/admin: путь по умолчанию — от расположения скрипта, не от рабочего каталога.
// Копия скрипта в «репо-фикстуре» TMP/<имя>/scripts — путь по умолчанию у неё TMP/<имя>/apps/admin/dist;
// настоящий apps/admin/dist (его оставляет ./task ci) тест не читает (ревью UF1b/I, I1).
function fakeRepo(files) {
  const root = join(TMP, `repo${n++}`);
  mkdirSync(join(root, 'scripts'), { recursive: true });
  copyFileSync(SCRIPT, join(root, 'scripts', 'check-admin-dist.mjs'));
  for (const [name, body] of Object.entries(files ?? {})) {
    const f = join(root, 'apps', 'admin', 'dist', name);
    mkdirSync(dirname(f), { recursive: true });
    writeFileSync(f, body);
  }
  return root;
}
const runScript = (script, cwd) => {
  const r = spawnSync(process.execPath, [script], { cwd, encoding: 'utf8' });
  return { status: r.status, dir: `${r.stdout}${r.stderr}`.match(/каталог: (.+)/)?.[1]?.trim() };
};

test('запуск из другого каталога — тот же dist (годный и отсутствующий)', () => {
  assert.equal(DEFAULT_DIST, join(ROOT, 'apps', 'admin', 'dist'));
  const good = {
    'index.html': '<!doctype html>',
    'assets/index.js': 'console.log(1)',
    'assets/index.css': CLEAN_CSS,
  };
  for (const [files, status] of [
    [good, 0],
    [undefined, 1],
  ]) {
    const root = fakeRepo(files);
    const script = join(root, 'scripts', 'check-admin-dist.mjs');
    const expected = join(root, 'apps', 'admin', 'dist');
    for (const cwd of [root, join(root, 'scripts'), TMP]) {
      const r = runScript(script, cwd);
      assert.equal(r.status, status, `код выхода из ${cwd}`);
      assert.equal(r.dir, expected, `каталог в выводе из ${cwd}`);
    }
  }
});

// Ревью UF1b/I, m1: Node запускает главный модуль по настоящему пути (realpath); путь через junction или
// симлинк не должен превращать проверку в молчаливый выход с кодом 0.
test('запуск по пути через junction — проверка идёт, а не молчит', (t) => {
  const root = fakeRepo();
  const link = join(TMP, `link${n++}`);
  try {
    symlinkSync(join(root, 'scripts'), link, 'junction');
  } catch (e) {
    t.skip(`junction не создан: ${e.code}`);
    return;
  }
  const r = runScript(join(link, 'check-admin-dist.mjs'), TMP);
  assert.equal(r.status, 1);
  assert.equal(r.dir, join(root, 'apps', 'admin', 'dist'));
});
