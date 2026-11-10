// Проверка собранной админки (план UF1b.4, U4; спека UI-фундамента §2.1, §5.4):
//   node scripts/check-admin-dist.mjs [<каталог>]   # по умолчанию apps/admin/dist — от расположения скрипта
// Вызывается в admin:ci сразу после build (и в scripts/e2e-admin.sh после сборки прод-проекта, UF1b.7).
// - прод-CSS = dev-CSS: в CSS есть light-dark( и нет полифила --lightningcss-* (build.cssTarget, I2);
// - шрифты — файлами: data:font/… в CSS staticCSP (font-src 'self') отвергает (build.assetsInlineLimit, UF1b.7);
// - моки не попали в обычную сборку (вторая линия после стража графа src/shots/shots.test.ts): в *.js, *.html,
//   *.css нет меток mockServiceWorker, msw/browser, setupWorker, а файла mockServiceWorker.js нет вовсе.
// Читаются только *.js, *.html, *.css: в двоичных woff2 (@fontsource) подстрока «msw» нашлась бы случайно.
// Нет каталога, нет ни одного *.js или *.css — ошибка, а не «чисто» (проверка не после той сборки).
import { existsSync, readdirSync, readFileSync, realpathSync, statSync } from 'node:fs';
import { basename, dirname, extname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

export const DEFAULT_DIST = resolve(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  'apps',
  'admin',
  'dist',
);
export const MSW_MARKERS = ['mockServiceWorker', 'msw/browser', 'setupWorker'];
const TEXT = new Set(['.js', '.html', '.css']);

export function checkDist(dir) {
  if (!existsSync(dir) || !statSync(dir).isDirectory())
    return [`нет каталога ${dir} — сначала сборка`];
  // строки путей, а не Dirent.parentPath (Node ≥ 20.12): admin:ci зовёт голый node раннера (ревью UF1b/I, m4)
  const files = readdirSync(dir, { recursive: true })
    .map((f) => join(dir, String(f)))
    .filter((f) => statSync(f).isFile());
  const rel = (f) => relative(dir, f).split('\\').join('/');
  const bad = [];
  const text = files.filter((f) => TEXT.has(extname(f).toLowerCase()));
  const css = text.filter((f) => extname(f).toLowerCase() === '.css');
  if (!text.some((f) => extname(f).toLowerCase() === '.js'))
    bad.push(`${dir}: нет *.js — не сборка админки`);
  if (css.length === 0) bad.push(`${dir}: нет *.css — не сборка админки`);

  for (const f of files)
    if (basename(f) === 'mockServiceWorker.js') bad.push(`${rel(f)}: файл воркера MSW в сборке`);
  let lightDark = false;
  for (const f of text) {
    const body = readFileSync(f, 'utf8');
    for (const m of MSW_MARKERS) if (body.includes(m)) bad.push(`${rel(f)}: ${m}`);
    if (css.includes(f)) {
      if (body.includes('--lightningcss-'))
        bad.push(
          `${rel(f)}: полифил --lightningcss-* вместо light-dark() — проверьте build.cssTarget`,
        );
      if (body.includes('light-dark(')) lightDark = true;
      if (body.includes('data:font/'))
        bad.push(
          `${rel(f)}: шрифт встроен как data:font/… — staticCSP (font-src 'self') его отвергнет; проверьте build.assetsInlineLimit`,
        );
    }
  }
  if (css.length > 0 && !lightDark)
    bad.push(`${dir}: в CSS нет light-dark( — токены тем не попали в сборку`);
  return bad;
}

// Главный модуль — по настоящим путям: Node запускает его по realpath, и путь через симлинк или junction
// иначе дал бы молчаливый выход с кодом 0 — вакуумно зелёный шаг admin:ci (ревью UF1b/I, m1).
function isMain() {
  try {
    return realpathSync(process.argv[1]) === realpathSync(fileURLToPath(import.meta.url));
  } catch {
    return false;
  }
}

if (process.argv[1] && isMain()) {
  const dir = process.argv[2] ? resolve(process.argv[2]) : DEFAULT_DIST;
  const bad = checkDist(dir);
  // каталог — при любом исходе: по нему видно, что проверено (ревью UF1b/I, I1)
  if (bad.length > 0) {
    console.error(`check-admin-dist: сборка админки не прошла проверку
каталог: ${dir}`);
    for (const b of bad) console.error(`  - ${b}`);
    process.exit(1);
  }
  console.log(`check-admin-dist: light-dark() без полифила, шрифты файлами, моков MSW нет
каталог: ${dir}`);
}
