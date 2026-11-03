#!/usr/bin/env node
// Подсадка бага для проверки стражей (CLAUDE.md, правило 7): временно ломает строку в файле, гоняет команду
// (обычно тест) и при любом исходе возвращает файл байт в байт со сверкой SHA-256.
//
//   node scripts/plant.mjs --file <путь> --find <строка> --replace <строка> [--nth N] [--timeout 60]
//        [--label <имя>] [--cwd <каталог>] [--expect <имя теста>] [--tail 10] [--no-baseline] [--budget 540]
//        [--regen <команда bash> --gen <путь>…] -- <команда> [аргументы…]
//   node scripts/plant.mjs --spec <json> [--budget 540] [--no-baseline]
//        # [{file, find, replace, cmd: [...], nth?, label?, timeout?, cwd?, expect?, regen?, gen?, baseline?}]
//        # или {defaults: {...}, plants: [...]}
//   node scripts/plant.mjs --recover [--force]   # вернуть файлы, брошенные жёстко убитыми запусками
//
// Итог строкой: KILLED (команда упала — страж поймал подсадку) | SURVIVED (прошла — тест вакуумен) | TIMEOUT |
// ERROR | NOT RUN (не влезла в бюджет). Код выхода: 0 — все KILLED, 1 — иначе, 130 — прерван сигналом.
//
// Достоверность KILLED: до первой подсадки команда гоняется на исходнике (база, один раз на команду+cwd) и
// должна пройти — иначе «красная без подсадки», подсадок с ней нет. --expect — имя теста, которое должно быть
// среди упавших по формату вывода (go test «--- FAIL: <имя> (», vitest «FAIL … > <имя>», node:test «✖ <имя> (»),
// а не подстрока: «=== RUN», «--- PASS» и TestCreateDuplicate для TestCreate не засчитываются.
//
// Бюджет (--budget, по умолчанию 540 с — под потолок Bash-инструмента 600 с): подсадка, которой может не
// хватить остатка (таймаут × (команда + 2 regen); база — по факту, остаток перепроверяется после неё), не
// начинается — NOT RUN. Жёсткое убийство посреди подсадки оставило бы файл с мутантом.
//
// Безопасность файла: исходник — в памяти и в бэкапе .tools/tmp/plant/<ключ>-<pid>-<случайное>.bak, рядом манифест
// <ключ>.json (он же замок файла; владелец обновляет его mtime раз в 5 с — пульс). Если процесс убит жёстко
// (таймаут инструмента, taskkill /F), след остаётся; его восстанавливает следующий запуск или --recover, если
// владелец мёртв (pid или пульс старше 30 с) и в файле всё ещё подсадка. --recover называет живые следы и
// выходит с 1; --force снимает и их.

import { spawn, spawnSync } from 'node:child_process';
import { createHash, randomBytes } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const IS_WIN = process.platform === 'win32';
export const DEFAULT_TIMEOUT_S = 60;
export const DEFAULT_TAIL = 10;
export const DEFAULT_BUDGET_S = 540;
// После выхода команды ждём закрытия её вывода не дольше этого: внук, унаследовавший пайп, не должен вешать скрипт.
const CLOSE_GRACE_MS = 2000;
const OUTPUT_CAP = 16 * 1024 * 1024;
const HEARTBEAT_MS = 5000;
export const STALE_MS = 30_000;

const USAGE =
  'использование: node scripts/plant.mjs --file <путь> --find <строка> --replace <строка> [--nth N] ' +
  '[--timeout 60] [--label <имя>] [--cwd <каталог>] [--expect <имя теста>] [--no-baseline] [--budget 540] ' +
  '[--regen <команда> --gen <путь>] -- <команда> [аргументы…] | --spec <json> | --recover [--force]';

export const plantDir = (env = process.env) =>
  env.WF_PLANT_DIR ? path.resolve(env.WF_PLANT_DIR) : path.join(ROOT, '.tools', 'tmp', 'plant');

const sha256 = (buf) => createHash('sha256').update(buf).digest('hex');
const short = (s) => (s.length > 60 ? `${s.slice(0, 57)}…` : s).replace(/\r?\n/g, '⏎');
const rel = (p) => path.relative(process.cwd(), p).split(path.sep).join('/') || '.';

// Ключ замка: канонический путь (регистр Windows не различает).
export function lockKey(file) {
  let p = path.resolve(file);
  try {
    p = fs.realpathSync.native(p);
  } catch {
    // нет файла — ключ по пути как есть; ошибку «нет файла» скажет чтение
  }
  return sha256(IS_WIN ? p.toLowerCase() : p).slice(0, 24);
}

function indexesOf(buf, needle) {
  const out = [];
  for (let i = buf.indexOf(needle); i !== -1; i = buf.indexOf(needle, i + 1)) out.push(i);
  return out;
}

// Буквальная подстановка на уровне байтов: BOM, кодировка и переводы строк вне вхождения не трогаются.
export function applyPlant(content, find, replace, nth) {
  if (!find) throw new Error('--find пустой');
  if (find === replace) throw new Error('--replace совпадает с --find: подсадка ничего не меняет');
  let f = Buffer.from(find);
  let r = Buffer.from(replace);
  let at = indexesOf(content, f);
  // Многострочный find из JSON приходит с \n, а файл может быть в CRLF.
  if (at.length === 0 && find.includes('\n') && content.includes('\r\n')) {
    f = Buffer.from(find.replace(/\r?\n/g, '\r\n'));
    r = Buffer.from(replace.replace(/\r?\n/g, '\r\n'));
    at = indexesOf(content, f);
  }
  if (at.length === 0) throw new Error(`строка не найдена: «${short(find)}»`);
  if (nth === undefined) {
    if (at.length !== 1)
      throw new Error(`строка встречается ${at.length} раз(а) — укажите --nth 1…${at.length}`);
    nth = 1;
  } else if (!(nth >= 1 && nth <= at.length)) {
    throw new Error(`--nth ${nth} вне диапазона 1…${at.length}`);
  }
  const i = at[nth - 1];
  return Buffer.concat([content.subarray(0, i), r, content.subarray(i + f.length)]);
}

// --- упавшие тесты по выводу ---

// ANSI-раскраска vitest/go в выводе.
const ANSI = /\x1b\[[0-9;]*m/g;

// Имена упавших тестов: go test («--- FAIL: TestX/sub (0.00s)», в т. ч. с -v), vitest (« FAIL  file > a > b»,
// «× a > b 5ms»), node:test (spec «✖ имя (1.2ms)», TAP «not ok 3 - имя»).
export function failedTests(output) {
  const names = [];
  for (const raw of output.replace(ANSI, '').split(/\r?\n/)) {
    const line = raw.trim();
    let m;
    if ((m = /^--- FAIL: (\S+) \(/.exec(line))) names.push(m[1]);
    else if ((m = /^FAIL\s+\S+ > (.+)$/.exec(line))) names.push(m[1].trim());
    else if ((m = /^[×✗] (.+?)(?: \d+(?:\.\d+)?m?s)?$/.exec(line))) names.push(m[1]);
    else if ((m = /^✖ (.+) \([\d.]+m?s\)$/.exec(line))) names.push(m[1]);
    else if ((m = /^not ok \d+ - (.+?)(?:\s+#.*)?$/.exec(line))) names.push(m[1]);
  }
  return names;
}

// Совпадение по границе имени: точное, подтест go («TestX/…»), хвост цепочки vitest («… > имя»).
export function expectMatches(output, name) {
  return failedTests(output).some(
    (n) => n === name || n.startsWith(`${name}/`) || n.endsWith(` > ${name}`),
  );
}

// --- разбор аргументов ---

const VALUE_FLAGS = new Set([
  'file',
  'find',
  'replace',
  'nth',
  'timeout',
  'label',
  'cwd',
  'expect',
  'regen',
  'gen',
  'tail',
  'spec',
  'budget',
]);

function normalizePlant(raw, where) {
  const p = { ...raw };
  const need = (k) => {
    if (typeof p[k] !== 'string') throw new Error(`${where}: нужен --${k}`);
  };
  need('file');
  need('find');
  need('replace');
  if (!Array.isArray(p.cmd) || p.cmd.length === 0 || !p.cmd.every((a) => typeof a === 'string')) {
    throw new Error(`${where}: нет команды (после -- или cmd: [...])`);
  }
  if (p.nth !== undefined) {
    p.nth = Number(p.nth);
    if (!Number.isInteger(p.nth) || p.nth < 1) throw new Error(`${where}: --nth — целое ≥ 1`);
  }
  p.timeout = p.timeout === undefined ? DEFAULT_TIMEOUT_S : Number(p.timeout);
  if (!(p.timeout > 0)) throw new Error(`${where}: --timeout — секунды > 0`);
  p.tail = p.tail === undefined ? DEFAULT_TAIL : Number(p.tail);
  if (!Number.isInteger(p.tail) || p.tail < 0) throw new Error(`${where}: --tail — целое ≥ 0`);
  p.label = p.label || rel(path.resolve(p.file));
  p.baseline = p.baseline !== false;
  if (typeof p.regen === 'string') p.regen = ['bash', '-c', p.regen];
  if (p.regen !== undefined && (!Array.isArray(p.regen) || p.regen.length === 0)) {
    throw new Error(`${where}: regen — строка (bash -c) или массив`);
  }
  p.gen = p.gen ?? [];
  if (!Array.isArray(p.gen)) p.gen = [p.gen];
  if (p.regen && p.gen.length === 0)
    throw new Error(`${where}: с --regen нужен --gen <сгенерированный путь>`);
  if (!p.regen && p.gen.length > 0) throw new Error(`${where}: --gen без --regen`);
  return p;
}

export function parseArgs(argv) {
  const opts = { gen: [] };
  let cmd = null;
  let recover = false;
  let force = false;
  let noBaseline = false;
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--') {
      cmd = argv.slice(i + 1);
      break;
    }
    if (a === '--recover' || a === '--force' || a === '--no-baseline') {
      if (a === '--recover') recover = true;
      else if (a === '--force') force = true;
      else noBaseline = true;
      continue;
    }
    const m = /^--([a-z]+)(?:=(.*))?$/s.exec(a);
    if (!m || !VALUE_FLAGS.has(m[1])) throw new Error(`неизвестный аргумент ${a}\n${USAGE}`);
    let v = m[2];
    if (v === undefined) {
      if (i + 1 >= argv.length) throw new Error(`--${m[1]} без значения`);
      v = argv[++i];
    }
    if (m[1] === 'gen') opts.gen.push(v);
    else opts[m[1]] = v;
  }
  if (force && !recover) throw new Error('--force — только с --recover');
  if (recover) return { mode: 'recover', plants: [], force };
  const budget = opts.budget === undefined ? DEFAULT_BUDGET_S : Number(opts.budget);
  if (!(budget > 0)) throw new Error('--budget — секунды > 0');
  const extra = noBaseline ? { baseline: false } : {};
  if (opts.spec !== undefined)
    return { mode: 'spec', spec: opts.spec, budget, plants: readSpec(opts.spec, extra) };
  const { spec: _spec, budget: _budget, ...rest } = opts;
  if (rest.gen.length === 0) delete rest.gen;
  return {
    mode: 'single',
    budget,
    plants: [normalizePlant({ ...rest, ...extra, cmd }, 'аргументы')],
  };
}

function readSpec(file, extra) {
  const data = JSON.parse(fs.readFileSync(file, 'utf8'));
  const list = Array.isArray(data) ? data : data.plants;
  const defaults = Array.isArray(data) ? {} : (data.defaults ?? {});
  if (!Array.isArray(list) || list.length === 0)
    throw new Error(`${file}: нужен непустой массив подсадок`);
  return list.map((p, i) => normalizePlant({ ...defaults, ...extra, ...p }, `${file}[${i}]`));
}

// --- процессы ---

const isAlive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch (e) {
    return e.code === 'EPERM';
  }
};

function killTree(pid) {
  if (IS_WIN) {
    spawnSync('taskkill', ['/pid', String(pid), '/T', '/F'], {
      windowsHide: true,
      stdio: 'ignore',
    });
    return;
  }
  try {
    process.kill(-pid, 'SIGKILL'); // своя группа: spawn с detached; группа живёт и после выхода лидера
  } catch {
    try {
      process.kill(pid, 'SIGKILL');
    } catch {
      // уже завершился
    }
  }
}

function quoteCmd(a) {
  if (a === '') return '""';
  return /[\s"&|<>^()%!,;=]/.test(a) ? `"${a.replace(/"/g, '""')}"` : a;
}

// Windows: .exe — напрямую; .cmd/.bat (pnpm и т. п.) — через cmd.exe с кавычками; sh-скрипт без расширения
// (./task) — через bash. POSIX — как есть.
export function resolveCommand(cmd, cwd, env = process.env) {
  if (!IS_WIN) return { file: cmd[0], args: cmd.slice(1), shell: false };
  const [name, ...args] = cmd;
  const exts = (env.PATHEXT || '.COM;.EXE;.BAT;.CMD').split(';').filter(Boolean);
  const hasExt = exts.some((e) => name.toLowerCase().endsWith(e.toLowerCase()));
  // Путь с разделителем — от cwd команды; голое имя — только по PATH.
  const dirs = /[\\/]/.test(name)
    ? [cwd]
    : (env.PATH || env.Path || '').split(path.delimiter).filter(Boolean);
  for (const dir of dirs) {
    const base = path.resolve(dir, name);
    for (const cand of hasExt ? [base] : [...exts.map((e) => base + e), base]) {
      let st;
      try {
        st = fs.statSync(cand);
      } catch {
        continue;
      }
      if (!st.isFile()) continue;
      const ext = path.extname(cand).toLowerCase();
      if (ext === '.cmd' || ext === '.bat') {
        return { file: [cand, ...args].map(quoteCmd).join(' '), args: [], shell: true };
      }
      if (ext === '.exe' || ext === '.com') return { file: cand, args, shell: false };
      if (name === 'bash') break;
      return resolveCommand(['bash', cand, ...args], cwd, env);
    }
  }
  return { file: name, args, shell: false };
}

// Команда с таймаутом и отменой; вывод (stdout+stderr) собирается в память, дерево процессов убивается целиком.
function runCommand(cmd, { cwd, timeoutMs, signal, onSpawn }) {
  return new Promise((done) => {
    const started = Date.now();
    const chunks = [];
    let size = 0;
    let timedOut = false;
    let aborted = false;
    let finished = false;
    let child;
    const finish = (res) => {
      if (finished) return;
      finished = true;
      clearTimeout(timer);
      signal?.removeEventListener('abort', onAbort);
      done({
        ...res,
        timedOut,
        aborted,
        ms: Date.now() - started,
        output: Buffer.concat(chunks).toString('utf8'),
      });
    };
    // Только пока свой дочерний не завершён: после выхода его pid может достаться чужому процессу.
    const kill = () =>
      child?.pid && child.exitCode === null && child.signalCode === null && killTree(child.pid);
    const onAbort = () => {
      aborted = true;
      kill();
    };
    const timer = setTimeout(() => {
      timedOut = true;
      kill();
    }, timeoutMs);
    try {
      const r = resolveCommand(cmd, cwd);
      child = spawn(r.file, r.args, {
        cwd,
        shell: r.shell,
        detached: !IS_WIN,
        windowsHide: true,
        stdio: ['ignore', 'pipe', 'pipe'],
      });
    } catch (error) {
      finish({ code: null, error });
      return;
    }
    const collect = (d) => {
      size += d.length;
      chunks.push(d);
      while (size > OUTPUT_CAP && chunks.length > 1) size -= chunks.shift().length;
    };
    child.stdout.on('data', collect);
    child.stderr.on('data', collect);
    child.on('error', (error) => finish({ code: null, error }));
    child.on('exit', (code, sig) => {
      // Внук держит пайп после выхода команды — не ждём закрытия вывода. Не добиваем: pid команды уже
      // свободен; добивание — только при таймауте и отмене, пока команда жива.
      const t = setTimeout(() => {
        child.stdout.destroy();
        child.stderr.destroy();
        finish({ code, sig });
      }, CLOSE_GRACE_MS);
      child.on('close', () => {
        clearTimeout(t);
        finish({ code, sig });
      });
    });
    signal?.addEventListener('abort', onAbort, { once: true });
    if (signal?.aborted) onAbort();
    onSpawn?.(child);
  });
}

// --- след подсадки: манифест + бэкап ---

const unlinkQuiet = (p) => {
  try {
    fs.unlinkSync(p);
  } catch {
    // уже убран
  }
};

function readManifest(p) {
  try {
    return JSON.parse(fs.readFileSync(p, 'utf8'));
  } catch {
    return null;
  }
}

function readQuiet(p) {
  try {
    return fs.readFileSync(p);
  } catch {
    return null;
  }
}

// Владелец следа жив: pid существует и пульс (mtime манифеста) свежий. Переиспользованный Windows pid без
// пульса — мёртвый владелец.
function ownerAlive(manifestPath, m) {
  if (m.pid === process.pid) return false;
  if (!isAlive(m.pid)) return false;
  try {
    return Date.now() - fs.statSync(manifestPath).mtimeMs < STALE_MS;
  } catch {
    return false;
  }
}

function fileState(m) {
  const cur = readQuiet(m.file);
  const s = cur && sha256(cur);
  if (!m.plantedSha || s === m.originalSha) return 'исходный';
  return s === m.plantedSha ? 'с подсадкой' : 'изменён';
}

// Вернуть файл по следу мёртвого запуска. Трогаем, только если в файле всё ещё именно подсадка.
function recoverOne(manifestPath, m) {
  const label = m.label ?? path.basename(manifestPath);
  const cur = readQuiet(m.file);
  const curSha = cur && sha256(cur);
  const sameTrace = () => {
    const again = readManifest(manifestPath);
    return again && again.pid === m.pid && again.started === m.started;
  };
  if (!m.plantedSha) {
    // Умер между замком и подсадкой: файл не тронут.
    if (sameTrace()) unlinkQuiet(manifestPath);
    return { ok: true, msg: `след ${label}: замок без подсадки снят, ${rel(m.file)} не тронут` };
  }
  if (curSha === m.originalSha) {
    if (sameTrace()) {
      unlinkQuiet(m.backup);
      unlinkQuiet(manifestPath);
    }
    return { ok: true, msg: `след ${label}: файл ${rel(m.file)} уже исходный, след убран` };
  }
  if (curSha !== m.plantedSha) {
    return {
      ok: false,
      msg: `ERROR ${label}: файл ${rel(m.file)} изменён после подсадки — не трогаю; бэкап ${m.backup}`,
    };
  }
  const bak = readQuiet(m.backup);
  if (!bak || sha256(bak) !== m.originalSha) {
    return {
      ok: false,
      msg: `ERROR ${label}: бэкап ${m.backup} отсутствует или испорчен — ${rel(m.file)} не тронут`,
    };
  }
  fs.writeFileSync(m.file, bak);
  if (sha256(fs.readFileSync(m.file)) !== m.originalSha) {
    return {
      ok: false,
      msg: `ERROR ${label}: после возврата SHA-256 ${rel(m.file)} не исходный; бэкап ${m.backup}`,
    };
  }
  if (sameTrace()) {
    unlinkQuiet(m.backup);
    unlinkQuiet(manifestPath);
  }
  return { ok: true, msg: `след ${label}: ${rel(m.file)} восстановлен из бэкапа` };
}

// Следы с мёртвыми владельцами восстанавливаются; живые (pid есть, пульс свежий) — чужая работа: не трогаем,
// но возвращаем как live, чтобы --recover их назвал. force — снять и живые.
function recoverAll(dir, { force = false } = {}) {
  let names;
  try {
    names = fs.readdirSync(dir);
  } catch {
    return [];
  }
  const out = [];
  for (const n of names.filter((x) => x.endsWith('.json'))) {
    const mp = path.join(dir, n);
    const m = readManifest(mp);
    if (!m) {
      out.push({ ok: false, msg: `ERROR ${n}: манифест не читается — разберите ${mp} вручную` });
      continue;
    }
    if (!force && ownerAlive(mp, m)) {
      out.push({
        ok: false,
        live: true,
        msg:
          `ERROR ${m.label}: след занят живым pid ${m.pid}, файл ${rel(m.file)} — ${fileState(m)}; ` +
          'если подсадок сейчас никто не гоняет — node scripts/plant.mjs --recover --force',
      });
      continue;
    }
    out.push(recoverOne(mp, m));
  }
  // Бэкап без манифеста — процесс умер до подсадки: файл не тронут, бэкап лишний.
  const live = new Set(
    names.filter((x) => x.endsWith('.json')).map((x) => readManifest(path.join(dir, x))?.backup),
  );
  for (const n of names.filter((x) => x.endsWith('.bak'))) {
    const p = path.join(dir, n);
    const pid = Number(/-(\d+)-[^-]+\.bak$/.exec(n)?.[1]);
    if (!live.has(p) && pid && !isAlive(pid)) unlinkQuiet(p);
  }
  return out;
}

function hashTree(paths) {
  const out = new Map();
  const walk = (p) => {
    let st;
    try {
      st = fs.statSync(p);
    } catch {
      return;
    }
    if (st.isDirectory()) for (const n of fs.readdirSync(p).sort()) walk(path.join(p, n));
    else out.set(rel(p), sha256(fs.readFileSync(p)));
  };
  for (const p of paths) walk(path.resolve(p));
  return out;
}

function diffTrees(a, b) {
  const keys = new Set([...a.keys(), ...b.keys()]);
  return [...keys].filter((k) => a.get(k) !== b.get(k)).sort();
}

const baselineKey = (p) => JSON.stringify([path.resolve(p.cwd ?? '.'), p.cmd]);

// Сколько секунд подсадка (файл с мутантом) может занять в худшем случае: команда и два regen. База в это не
// входит: она идёт на исходнике (жёсткое убийство на ней файл не портит) и вычитается по факту — бюджет
// перепроверяется после неё.
const worstCase = (p) => p.timeout * (1 + (p.regen ? 2 : 0));

const notRun = (p, left, need, series) => ({
  label: p.label,
  file: rel(path.resolve(p.file)),
  status: 'NOT RUN',
  exit: null,
  ms: 0,
  output: '',
  msg:
    `бюджет: осталось ${Math.max(0, left).toFixed(0)} с, нужно до ${need} с` +
    (series
      ? ' — разбейте серию на несколько вызовов'
      : ' — уменьшите --timeout или поднимите --budget'),
});

// --- одна подсадка ---

async function runPlant(p, ctx) {
  const res = {
    label: p.label,
    file: rel(path.resolve(p.file)),
    status: 'ERROR',
    exit: null,
    ms: 0,
    msg: '',
    output: '',
  };
  const fail = (msg) => Object.assign(res, { status: 'ERROR', msg });
  const abs = path.resolve(p.file);
  const cwd = path.resolve(p.cwd ?? '.');
  // Замок — до чтения исходника: иначе вторая подсадка того же файла могла бы прочесть чужую подсадку
  // как «исходник» и потом «вернуть» её. Манифест без plantedSha — «замок взят, файл не тронут».
  fs.mkdirSync(ctx.dir, { recursive: true });
  const key = lockKey(abs);
  const manifestPath = path.join(ctx.dir, `${key}.json`);
  const manifest = {
    file: abs,
    pid: process.pid,
    label: p.label,
    started: new Date().toISOString(),
  };
  const writeManifest = (flag) =>
    fs.writeFileSync(manifestPath, JSON.stringify(manifest, null, 2), { flag });
  try {
    writeManifest('wx');
  } catch (e) {
    if (e.code !== 'EEXIST') return fail(e.message);
    const other = readManifest(manifestPath);
    const who = other ? ` (pid ${other.pid}, «${other.label}»)` : '';
    const dead =
      other && !ownerAlive(manifestPath, other)
        ? ' — владелец мёртв, след не восстановлен: node scripts/plant.mjs --recover'
        : '';
    return fail(`файл ${res.file} занят другой подсадкой${who}${dead}`);
  }
  // Пульс: живой владелец обновляет mtime манифеста — по нему отличают переиспользованный pid.
  const beat = setInterval(() => {
    try {
      const now = new Date();
      fs.utimesSync(manifestPath, now, now);
    } catch {
      // манифест уже снят
    }
  }, HEARTBEAT_MS);
  beat.unref();
  try {
    return await plantLocked(p, ctx, {
      res,
      fail,
      abs,
      cwd,
      key,
      manifestPath,
      manifest,
      writeManifest,
    });
  } finally {
    clearInterval(beat);
  }
}

async function plantLocked(
  p,
  ctx,
  { res, fail, abs, cwd, key, manifestPath, manifest, writeManifest },
) {
  let original;
  let planted;
  try {
    original = fs.readFileSync(abs);
    planted = applyPlant(original, p.find, p.replace, p.nth);
  } catch (e) {
    unlinkQuiet(manifestPath);
    return fail(e.code === 'ENOENT' ? `нет файла ${res.file}` : e.message);
  }
  const run = (cmd) =>
    runCommand(cmd, { cwd, timeoutMs: p.timeout * 1000, signal: ctx.signal, onSpawn: ctx.onSpawn });

  // База: команда на исходнике (файл под нашим замком) должна пройти, иначе KILLED ничего не доказывает.
  if (p.baseline) {
    const bk = baselineKey(p);
    if (!ctx.baselines.has(bk)) {
      const b = await run(p.cmd);
      ctx.baselines.set(bk, b);
    }
    const b = ctx.baselines.get(bk);
    if (b.aborted || b.timedOut || b.error || b.code !== 0) {
      unlinkQuiet(manifestPath);
      res.output = b.output;
      if (b.aborted) return fail(`прервано (${ctx.signal.reason ?? 'сигнал'}) на базовом прогоне`);
      const why = b.timedOut
        ? `таймаут > ${p.timeout} с`
        : b.error
          ? `не запустилась: ${b.error.code ?? b.error.message}`
          : `exit ${b.code ?? b.sig}`;
      return fail(`команда красная без подсадки (${why}) — подсадка ничего не докажет`);
    }
  }
  // Бюджет после базы — по фактически потраченному времени.
  const left = ctx.budgetLeft();
  if (left < worstCase(p)) {
    unlinkQuiet(manifestPath);
    return notRun(p, left, worstCase(p), ctx.series);
  }

  const genBefore = p.regen ? hashTree(p.gen) : null;
  const backup = path.join(ctx.dir, `${key}-${process.pid}-${randomBytes(4).toString('hex')}.bak`);
  fs.writeFileSync(backup, original);
  Object.assign(manifest, {
    originalSha: sha256(original),
    plantedSha: sha256(planted),
    backup,
  });
  writeManifest('w');

  const started = Date.now();
  try {
    (ctx.plantWrite ?? fs.writeFileSync)(abs, planted);
    let r = null;
    if (p.regen) {
      r = await run(p.regen);
      if (!r.timedOut && !r.aborted && (r.error || r.code !== 0)) {
        res.output = r.output;
        fail(`regen после подсадки упал (exit ${r.code ?? r.error?.code})`);
        r = null;
      }
    }
    if (!p.regen || r) {
      if (!r?.timedOut && !r?.aborted) r = await run(p.cmd);
      res.output = r.output;
      res.exit = r.code;
      if (r.aborted) fail(`прервано (${ctx.signal.reason ?? 'сигнал'})`);
      else if (r.timedOut) Object.assign(res, { status: 'TIMEOUT', msg: `> ${p.timeout} с` });
      else if (r.error) fail(`команда не запустилась: ${r.error.code ?? r.error.message}`);
      else if (r.code === 0) res.status = 'SURVIVED';
      else if (p.expect !== undefined && !expectMatches(r.output, p.expect)) {
        const seen = failedTests(r.output).slice(0, 5);
        fail(
          `команда упала (exit ${r.code ?? r.sig}), но «${p.expect}» нет среди упавших` +
            ` (${seen.length ? seen.join(', ') : 'упавших тестов в выводе нет'}) — упало не то`,
        );
      } else Object.assign(res, { status: 'KILLED', exit: r.code ?? r.sig });
    }
  } catch (e) {
    fail(`исключение посреди подсадки: ${e.message}`);
  } finally {
    res.ms = Date.now() - started;
    // Возврат — всегда, первым делом; сверка — по SHA-256 перечитанного файла.
    const cur = readQuiet(abs);
    let restored = false;
    try {
      (ctx.restoreWrite ?? fs.writeFileSync)(abs, original);
      restored = sha256(fs.readFileSync(abs)) === manifest.originalSha;
    } catch (e) {
      ctx.err(`ERROR ${p.label}: возврат не удался: ${e.message}\n`);
    }
    if (cur && !cur.equals(planted) && !cur.equals(original)) {
      // Файл правил кто-то ещё (форматтер, другой агент): команда шла не по подсадке, итог недостоверен.
      // Сохраняем после возврата и под защитой: сбой записи не должен мешать возврату исходника.
      const changed = `${backup}.changed`;
      try {
        (ctx.changedWrite ?? fs.writeFileSync)(changed, cur);
        fail(
          `файл менялся во время команды — итог недостоверен; изменённое сохранено в ${changed}`,
        );
      } catch (e) {
        fail(
          `файл менялся во время команды — итог недостоверен; изменённое сохранить не удалось: ${e.message}`,
        );
      }
    }
    if (!restored) {
      fail(
        `после возврата SHA-256 ${res.file} не совпал с исходным — файл НЕ восстановлен; бэкап ${backup}`,
      );
    } else {
      // regen после возврата — и при прерывании: сгенерированное с подсадкой не должно остаться.
      if (p.regen) {
        const r = await runCommand(p.regen, { cwd, timeoutMs: p.timeout * 1000 });
        const diff = diffTrees(genBefore, hashTree(p.gen));
        if (r.timedOut || r.error || r.code !== 0)
          fail(`regen после возврата упал (exit ${r.code})`);
        else if (diff.length > 0) {
          fail(`сгенерированное после возврата не совпало: ${diff.join(', ')}`);
          ctx.err(
            `ERROR ${p.label}: сгенерированное после возврата не совпало с исходным: ${diff.join(', ')}\n`,
          );
        }
      }
      unlinkQuiet(backup);
      unlinkQuiet(manifestPath);
    }
  }
  return res;
}

const secs = (ms) => `${(ms / 1000).toFixed(1)} с`;

function resultLine(r) {
  switch (r.status) {
    case 'KILLED':
      return `KILLED ${r.label} (exit ${r.exit}, ${secs(r.ms)})`;
    case 'SURVIVED':
      return `SURVIVED ${r.label} (${secs(r.ms)}) — команда прошла, страж не поймал подсадку`;
    case 'TIMEOUT':
      return `TIMEOUT ${r.label} (${r.msg}, дерево процессов убито)`;
    case 'NOT RUN':
      return `NOT RUN ${r.label} (${r.msg})`;
    default:
      return `ERROR ${r.label}: ${r.msg}`;
  }
}

function tail(output, n) {
  if (n === 0 || !output.trim()) return '';
  const lines = output.replace(/\s+$/, '').split(/\r?\n/);
  return lines
    .slice(-n)
    .map((l) => `  │ ${l}\n`)
    .join('');
}

const cell = (s) => String(s).replace(/\|/g, '\\|');

export async function main(argv, io = {}) {
  const out = io.out ?? ((t) => process.stdout.write(t));
  const err = io.err ?? ((t) => process.stderr.write(t));
  const signals = io.signals ?? process;
  const dir = plantDir();
  const t0 = Date.now();

  let parsed;
  try {
    parsed = parseArgs(argv);
  } catch (e) {
    out(`ERROR plant: ${e.message}\n`);
    return 1;
  }

  const recovered = recoverAll(dir, { force: parsed.force });
  if (parsed.mode === 'recover') {
    for (const r of recovered) err(`${r.msg}\n`);
    if (recovered.length === 0) err('следов подсадок нет\n');
    return recovered.every((r) => r.ok) ? 0 : 1;
  }
  // В обычном запуске живые следы — работа соседних исполнителей, не шум.
  for (const r of recovered.filter((x) => !x.live)) err(`${r.msg}\n`);

  const ctl = new AbortController();
  // SIGHUP — и на Windows: так приходит закрытие консоли, ~10 с на возврат.
  const sigs = IS_WIN
    ? ['SIGINT', 'SIGTERM', 'SIGBREAK', 'SIGHUP']
    : ['SIGINT', 'SIGTERM', 'SIGHUP'];
  const onSig = (s) => ctl.abort(typeof s === 'string' ? s : 'сигнал');
  for (const s of sigs) signals.on(s, onSig);
  const results = [];
  const baselines = new Map();
  try {
    for (const p of parsed.plants) {
      if (ctl.signal.aborted) break;
      const budgetLeft = () => parsed.budget - (Date.now() - t0) / 1000;
      const series = parsed.plants.length > 1;
      let r;
      if (budgetLeft() < worstCase(p)) {
        r = notRun(p, budgetLeft(), worstCase(p), series);
      } else {
        try {
          r = await runPlant(p, {
            dir,
            signal: ctl.signal,
            err,
            baselines,
            budgetLeft,
            series,
            onSpawn: io.onSpawn,
            plantWrite: io.plantWrite,
            restoreWrite: io.restoreWrite,
            changedWrite: io.changedWrite,
          });
        } catch (e) {
          r = {
            label: p.label,
            file: rel(path.resolve(p.file)),
            status: 'ERROR',
            ms: 0,
            output: '',
            msg: `исключение: ${e.message}`,
          };
        }
      }
      results.push(r);
      const line = `${resultLine(r)}\n`;
      if (parsed.mode === 'spec') err(line);
      else out(line);
      err(tail(r.output, p.tail));
    }
  } finally {
    for (const s of sigs) signals.off(s, onSig);
  }

  if (parsed.mode === 'spec') {
    out('| label | файл | итог | время |\n|---|---|---|---|\n');
    for (const r of results) {
      const verdict =
        r.status === 'KILLED'
          ? `KILLED (exit ${r.exit})`
          : r.status === 'ERROR'
            ? `ERROR: ${r.msg}`
            : r.status;
      out(`| ${cell(r.label)} | ${cell(r.file)} | ${cell(verdict)} | ${secs(r.ms)} |\n`);
    }
  }
  if (ctl.signal.aborted) return 130;
  return results.length > 0 && results.every((r) => r.status === 'KILLED') ? 0 : 1;
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  main(process.argv.slice(2)).then(
    (code) => (process.exitCode = code),
    (e) => {
      process.stdout.write(`ERROR plant: ${e.stack ?? e}\n`);
      process.exitCode = 1;
    },
  );
}
