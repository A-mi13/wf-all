// Снимки экранов админки для ревьюера (спека §5.4, план UF1a.1):
//   node scripts/admin-shots.mjs --out <каталог> [--role admin] [--widths 1440,768,390]
//     [--themes dark,light] [--locale ru|en] [--touch] <путь>…
// --touch — устройство с пальцем (hasTouch: pointer: coarse): «только с компьютера» решает устройство, а не
// ширина (спека UI-фундамента §3.2) — снимок 390 без него — узкое окно с мышью.
// (./task admin:shots -- … передаёт аргументы). Поднимает Vite админки в режиме shots (моки MSW в
// браузере на фикстурах тестов, порт 5176), Chromium Playwright снимает каждый путь × ширину × тему
// во всю страницу в <out>/<слаг-пути>-<ширина>-<тема>[-en].png. Код выхода ≠ 0 — неописанные
// запросы к API или ошибки консоли (список — в вывод). Vite останавливается всегда.
// Снимки временные: каталог — .superpowers/sdd/<сессия>/shots/<группа>/, не коммитятся.
import { spawn, spawnSync } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { createRequire } from 'node:module';
import { join, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

export const ROLES = ['owner', 'admin', 'moderator', 'setup', 'anonymous'];
export const THEMES = ['dark', 'light'];
export const LOCALES = ['ru', 'en'];
const PORT = 5176;
const HEIGHT = { 1440: 900, 768: 1024, 390: 844 };

const USAGE =
  'использование: node scripts/admin-shots.mjs --out <каталог> [--role admin] [--widths 1440,768,390] ' +
  '[--themes dark,light] [--locale ru|en] [--touch] <путь>…';

const list = (v) =>
  v
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);

export function parseArgs(argv) {
  const o = {
    out: undefined,
    role: 'admin',
    widths: [1440, 768, 390],
    themes: [...THEMES],
    locale: 'ru',
    touch: false,
    paths: [],
  };
  const flags = new Set(['--out', '--role', '--widths', '--themes', '--locale']);
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (!arg.startsWith('--')) {
      // Git Bash (MSYS) переписывает «/audit» в «C:/Program Files/Git/audit» при запуске не-MSYS программы
      if (/^[A-Za-z]:[\\/]/.test(arg)) {
        throw new Error(
          `путь «${arg}» — путь Windows: Git Bash переписал адрес. Запустите с MSYS_NO_PATHCONV=1 ` +
            `или без ведущего / (audit, geo/cities)\n${USAGE}`,
        );
      }
      o.paths.push(arg.startsWith('/') ? arg : `/${arg}`);
      continue;
    }
    if (arg === '--touch') {
      o.touch = true;
      continue;
    }
    const eq = arg.indexOf('=');
    const name = eq < 0 ? arg : arg.slice(0, eq);
    if (!flags.has(name)) throw new Error(`неизвестный флаг ${name}\n${USAGE}`);
    const value = eq < 0 ? argv[++i] : arg.slice(eq + 1);
    if (value === undefined || value === '') throw new Error(`у ${name} нет значения\n${USAGE}`);
    if (name === '--out') o.out = value;
    if (name === '--role') o.role = value;
    if (name === '--locale') o.locale = value;
    if (name === '--themes') o.themes = list(value);
    if (name === '--widths') {
      o.widths = list(value).map((w) => {
        const n = Number(w);
        if (!Number.isInteger(n) || n < 1)
          throw new Error(`ширина ${w} — не целое положительное число`);
        return n;
      });
    }
  }
  if (!o.out)
    throw new Error(
      `нужен --out <каталог> (например .superpowers/sdd/<сессия>/shots/<группа>)\n${USAGE}`,
    );
  if (!ROLES.includes(o.role)) throw new Error(`роль ${o.role} — не из ${ROLES.join(', ')}`);
  for (const t of o.themes)
    if (!THEMES.includes(t)) throw new Error(`тема ${t} — не из ${THEMES.join(', ')}`);
  if (!LOCALES.includes(o.locale))
    throw new Error(`язык ${o.locale} — не из ${LOCALES.join(', ')}`);
  if (o.themes.length === 0 || o.widths.length === 0)
    throw new Error(`пустой список тем или ширин\n${USAGE}`);
  if (o.paths.length === 0)
    throw new Error(`нужен хотя бы один путь (/audit, /geo/cities…)\n${USAGE}`);
  return o;
}

// Путь адреса → безопасное имя файла: только [A-Za-z0-9_-], прочее — дефис.
export function slug(path) {
  const s = path
    .replace(/[^A-Za-z0-9_-]+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '');
  return s || 'root';
}

export function planShots(o) {
  const seen = new Map();
  for (const p of o.paths) {
    const s = slug(p);
    if (seen.has(s) && seen.get(s) !== p)
      throw new Error(`пути ${seen.get(s)} и ${p} дают одно имя файла «${s}»`);
    seen.set(s, p);
  }
  const suffix = `${o.locale === 'ru' ? '' : `-${o.locale}`}${o.touch ? '-touch' : ''}`;
  return [...seen.entries()].flatMap(([s, path]) =>
    o.widths.flatMap((width) =>
      o.themes.map((theme) => ({
        path,
        width,
        theme,
        file: join(o.out, `${s}-${width}-${theme}${suffix}.png`),
      })),
    ),
  );
}

export function shotUrl(base, path, role, locale) {
  const url = new URL(path, base);
  url.searchParams.append('shots-role', role);
  if (locale !== 'ru') url.searchParams.append('shots-locale', locale);
  return url.href;
}

// Сообщение консоли страницы → текст проблемы или null. Ответ мока не-2xx (401 anonymous, 403 «нет прав»,
// 404 «не найден») Chromium сам пишет как error «Failed to load resource» — для /v1/* того же origin это
// штатный ответ моков (неописанный запрос ловят console.error MSW и __wfShots.unhandled); тот же текст
// для модуля, шрифта или чужого адреса — проблема.
export function consoleProblem(type, text, url, base) {
  if (type !== 'error') return null;
  if (text.startsWith('Failed to load resource')) {
    try {
      const u = new URL(url);
      if (u.origin === new URL(base).origin && u.pathname.startsWith('/v1/')) return null;
    } catch {
      // адреса нет — не наш ответ API
    }
  }
  return `console.error — ${text}${url ? ` (${url})` : ''}`;
}

// ——— запуск ———

const adminDir = resolve(import.meta.dirname, '../apps/admin');

async function waitForVite(child, base, ms = 90_000) {
  const until = Date.now() + ms;
  while (Date.now() < until) {
    if (child.exitCode !== null)
      throw new Error(`Vite завершился с кодом ${child.exitCode} (порт ${PORT} занят?)`);
    try {
      const r = await fetch(base);
      if (r.ok) return;
    } catch {
      // ещё не слушает
    }
    await new Promise((r) => setTimeout(r, 300));
  }
  throw new Error(`Vite не поднялся на ${base} за ${ms / 1000} с`);
}

// Windows: дерево процессов целиком (иначе брошенный Vite держит порт); POSIX — группа процессов.
function stopVite(child) {
  if (child.exitCode !== null || child.pid === undefined) return;
  if (process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], { stdio: 'ignore' });
  } else {
    try {
      process.kill(-child.pid, 'SIGTERM');
    } catch {
      child.kill('SIGTERM');
    }
  }
}

async function main(argv) {
  const o = parseArgs(argv);
  const plan = planShots(o);
  mkdirSync(o.out, { recursive: true });
  const require = createRequire(join(adminDir, 'package.json'));
  // vite — своим JS через node, не через pnpm и не .cmd (на Windows .cmd требует shell)
  const viteBin = join(require.resolve('vite/package.json'), '..', 'bin', 'vite.js');
  const pw = await import(pathToFileURL(require.resolve('@playwright/test')).href);
  const chromium = pw.chromium ?? pw.default.chromium;

  const base = `http://127.0.0.1:${PORT}`;
  const vite = spawn(process.execPath, [viteBin, '--mode', 'shots'], {
    cwd: adminDir,
    stdio: ['ignore', 'pipe', 'pipe'],
    detached: process.platform !== 'win32',
  });
  let viteLog = '';
  vite.stdout.on('data', (d) => (viteLog += d));
  vite.stderr.on('data', (d) => (viteLog += d));
  const stop = () => stopVite(vite);
  process.on('SIGINT', () => {
    stop();
    process.exit(130);
  });

  const problems = [];
  let browser;
  try {
    await waitForVite(vite, base);
    browser = await chromium.launch();
    for (const shot of plan) {
      const context = await browser.newContext({
        viewport: { width: shot.width, height: HEIGHT[shot.width] ?? 900 },
        colorScheme: shot.theme,
        hasTouch: o.touch,
        reducedMotion: 'reduce',
        locale: o.locale === 'en' ? 'en-US' : 'ru-RU',
        timezoneId: 'UTC',
      });
      // выбор темы — как у сотрудника (wf.theme, спека §4.12): его читает public/theme-init.js до первого кадра
      await context.addInitScript((theme) => {
        try {
          localStorage.setItem('wf.theme', theme);
        } catch {
          // приватный режим — тема по системе (colorScheme контекста)
        }
      }, shot.theme);
      const page = await context.newPage();
      const where = `${shot.path} @${shot.width} ${shot.theme}`;
      page.on('console', (m) => {
        const problem = consoleProblem(m.type(), m.text(), m.location().url, base);
        if (problem) problems.push(`${where}: ${problem}`);
      });
      page.on('pageerror', (e) => problems.push(`${where}: ${e.message}`));
      try {
        await page.goto(shotUrl(base, shot.path, o.role, o.locale));
        await page
          .locator('html[data-shots-ready]')
          .waitFor({ state: 'attached', timeout: 60_000 });
        await page.waitForLoadState('networkidle');
        const unhandled = await page.evaluate(() => window.__wfShots?.unhandled ?? -1);
        if (unhandled !== 0) problems.push(`${where}: неописанных запросов к API — ${unhandled}`);
        // fullPage в Chromium на время съёмки снимает эмуляцию касания (pointer: coarse → fine, событие
        // change, обратного события нет) — страница перерисовалась бы как на компьютере. С --touch окно
        // растягивается на всю высоту страницы и снимается без fullPage.
        if (o.touch) {
          const height = await page.evaluate(() => document.documentElement.scrollHeight);
          await page.setViewportSize({ width: shot.width, height });
        }
        await page.screenshot({
          path: shot.file,
          fullPage: !o.touch,
          animations: 'disabled',
          caret: 'hide',
        });
        console.log(`снимок ${shot.file}`);
      } catch (e) {
        problems.push(`${where}: ${e instanceof Error ? e.message : String(e)}`);
      } finally {
        await context.close();
      }
    }
  } catch (e) {
    problems.push(e instanceof Error ? e.message : String(e));
    if (viteLog) problems.push(`вывод Vite:\n${viteLog}`);
  } finally {
    await browser?.close();
    stop();
  }
  if (problems.length > 0) {
    console.error(`\nadmin-shots: проблемы (${problems.length}):\n- ${problems.join('\n- ')}`);
    return 1;
  }
  console.log(`\nadmin-shots: ${plan.length} снимков в ${o.out}`);
  return 0;
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).then(
    (code) => process.exit(code),
    (e) => {
      console.error(`admin-shots: ${e instanceof Error ? e.message : String(e)}`);
      process.exit(2);
    },
  );
}
