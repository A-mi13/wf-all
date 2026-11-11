import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { EventEmitter } from 'node:events';
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  utimesSync,
  writeFileSync,
} from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { after, before, test } from 'node:test';
import { setTimeout as delay } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';
import {
  applyPlant,
  expectMatches,
  failedTests,
  lockKey,
  main,
  parseArgs,
  STALE_MS,
} from './plant.mjs';

// Общий скрипт подсадки бага (S2): подстановка → команда → возврат байт в байт при любом исходе.
// Команды-фикстуры — node -e: тест не зависит от bash и работает в Git Bash и в CI.

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const SCRIPT = join(ROOT, 'scripts', 'plant.mjs');
const NODE = process.execPath;
const BOM = Buffer.from([0xef, 0xbb, 0xbf]);
let TMP;
let seq = 0;

before(() => {
  mkdirSync(join(ROOT, '.tools', 'tmp'), { recursive: true });
  TMP = mkdtempSync(join(ROOT, '.tools', 'tmp', 'plant-test-'));
});
// Повторы: на Windows только что убитые процессы и антивирус держат файлы (EBUSY) — без них хук падал.
after(() => rmSync(TMP, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 }));

const sha = (b) => createHash('sha256').update(b).digest('hex');

// Отдельный каталог на тест: файл-цель и свой каталог бэкапов (WF_PLANT_DIR).
function sandbox(name, content) {
  const dir = join(TMP, `c${++seq}`);
  mkdirSync(dir, { recursive: true });
  const file = join(dir, name);
  writeFileSync(file, content);
  return { dir, file, plantDir: join(dir, 'plant'), env: { WF_PLANT_DIR: join(dir, 'plant') } };
}

function runCli(args, env = {}) {
  return new Promise((done) => {
    const child = spawn(NODE, [SCRIPT, ...args], {
      cwd: ROOT,
      env: { ...process.env, ...env },
      windowsHide: true,
    });
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (d) => (stdout += d));
    child.stderr.on('data', (d) => (stderr += d));
    child.on('close', (code) => done({ code, stdout, stderr }));
  });
}

// Команда «тест»: падает, если видит подсадку в файле, — так проверяется, что команда шла по подсаженному файлу.
const failsIfPlanted = (file, marker = 'MUTANT') => [
  NODE,
  '-e',
  `process.exit(require('fs').readFileSync(${JSON.stringify(file)},'utf8').includes(${JSON.stringify(marker)})?1:0)`,
];

const isAlive = (pid) => {
  try {
    process.kill(pid, 0);
    return true;
  } catch (e) {
    return e.code === 'EPERM';
  }
};

async function waitDead(pid) {
  for (let i = 0; i < 100 && isAlive(pid); i++) await delay(50);
  return !isAlive(pid);
}

const leftovers = (plantDir) => (existsSync(plantDir) ? readdirSync(plantDir) : []);

// --- чистые функции ---

test('applyPlant: ровно одно вхождение заменяется, остальные байты не тронуты', () => {
  const src = Buffer.concat([BOM, Buffer.from('а := 1\nif a > 0 {\n')]);
  const out = applyPlant(src, 'a > 0', 'a >= 0');
  assert.deepEqual(out, Buffer.concat([BOM, Buffer.from('а := 1\nif a >= 0 {\n')]));
});

test('applyPlant: ноль, два вхождения, пустой find и find = replace — ошибка', () => {
  const src = Buffer.from('x == y; x == y');
  assert.throws(() => applyPlant(src, 'z', 'w'), /не найдена/);
  assert.throws(() => applyPlant(src, 'x == y', 'x != y'), /2 раз.*--nth/);
  assert.throws(() => applyPlant(src, '', 'w'), /пуст/);
  assert.throws(() => applyPlant(Buffer.from('ab'), 'a', 'a'), /совпадает/);
});

test('applyPlant: --nth выбирает вхождение; вне диапазона — ошибка', () => {
  const src = Buffer.from('x == y; x == y; x == y');
  assert.equal(applyPlant(src, 'x == y', 'Q', 2).toString(), 'x == y; Q; x == y');
  assert.equal(applyPlant(src, 'x == y', 'Q', 3).toString(), 'x == y; x == y; Q');
  assert.throws(() => applyPlant(src, 'x == y', 'Q', 4), /--nth 4.*1…3/);
  assert.throws(() => applyPlant(src, 'x == y', 'Q', 0), /--nth 0/);
});

test('applyPlant: CRLF в файле, \\n в find — подстановка с \\r\\n', () => {
  const src = Buffer.from('один\r\nif ok {\r\n\treturn\r\n}\r\n');
  const out = applyPlant(src, 'if ok {\n\treturn', 'if !ok {\n\treturn nil');
  assert.equal(out.toString(), 'один\r\nif !ok {\r\n\treturn nil\r\n}\r\n');
});

test('parseArgs: одиночная подсадка, команда после --, умолчания', () => {
  const o = parseArgs([
    '--file',
    'a.go',
    '--find',
    'x',
    '--replace=y',
    '--',
    'go',
    'test',
    '--',
    './x/',
  ]);
  assert.equal(o.mode, 'single');
  assert.equal(o.plants[0].file, 'a.go');
  assert.equal(o.plants[0].find, 'x');
  assert.equal(o.plants[0].replace, 'y');
  assert.deepEqual(o.plants[0].cmd, ['go', 'test', '--', './x/']);
  assert.equal(o.plants[0].timeout, 60);
  assert.equal(o.plants[0].label, 'a.go');
});

test('parseArgs: ошибки разбора', () => {
  assert.throws(() => parseArgs(['--file', 'a', '--find', 'x', '--replace', 'y']), /команд/);
  assert.throws(() => parseArgs(['--find', 'x', '--replace', 'y', '--', 'c']), /--file/);
  assert.throws(() => parseArgs(['--file', 'a', '--find', 'x', '--', 'c']), /--replace/);
  assert.throws(
    () => parseArgs(['--file', 'a', '--find', 'x', '--replace', 'y', '--bogus', '--', 'c']),
    /--bogus/,
  );
  assert.throws(
    () => parseArgs(['--file', 'a', '--find', 'x', '--replace', 'y', '--timeout', '0', '--', 'c']),
    /--timeout/,
  );
  assert.throws(
    () => parseArgs(['--file', 'a', '--find', 'x', '--replace', 'y', '--nth', '1.5', '--', 'c']),
    /--nth/,
  );
  assert.throws(
    () => parseArgs(['--file', 'a', '--find', 'x', '--replace', 'y', '--regen', 'sqlc', '--', 'c']),
    /--gen/,
  );
});

// --- целиком через CLI ---

test('KILLED: команда упала на подсадке; файл с BOM, CRLF и кириллицей восстановлен байт в байт', async () => {
  const content = Buffer.concat([
    BOM,
    Buffer.from('// проверка\r\nreturn ok == nil\n// хвост без перевода строки'),
  ]);
  const s = sandbox('a.go', content);
  const r = await runCli(
    [
      '--file',
      s.file,
      '--find',
      'ok == nil',
      '--replace',
      'MUTANT',
      '--label',
      'страж-1',
      '--',
      ...failsIfPlanted(s.file),
    ],
    s.env,
  );
  assert.match(r.stdout, /^KILLED страж-1 \(exit 1, \d+\.\d с\)$/m);
  assert.equal(r.code, 0);
  assert.equal(sha(readFileSync(s.file)), sha(content));
  assert.deepEqual(leftovers(s.plantDir), [], 'бэкап и манифест удалены');
});

test('SURVIVED: команда прошла — тест вакуумен, код 1, файл восстановлен', async () => {
  const content = 'без BOM: значение\n';
  const s = sandbox('b.txt', content);
  const r = await runCli(
    [
      '--file',
      s.file,
      '--find',
      'значение',
      '--replace',
      'MUTANT',
      '--',
      ...failsIfPlanted(s.file, 'НЕТ-ТАКОГО'),
    ],
    s.env,
  );
  assert.match(r.stdout, /^SURVIVED /m);
  assert.equal(r.code, 1);
  assert.equal(readFileSync(s.file, 'utf8'), content);
});

test('ноль вхождений — ERROR до запуска: команда не запускалась, файл не тронут', async () => {
  const s = sandbox('c.txt', 'abc\n');
  const marker = join(s.dir, 'ran');
  const r = await runCli(
    [
      '--file',
      s.file,
      '--find',
      'zzz',
      '--replace',
      'y',
      '--',
      NODE,
      '-e',
      `require('fs').writeFileSync(${JSON.stringify(marker)},'1')`,
    ],
    s.env,
  );
  assert.match(r.stdout, /^ERROR .*не найдена/m);
  assert.equal(r.code, 1);
  assert.equal(existsSync(marker), false);
  assert.equal(readFileSync(s.file, 'utf8'), 'abc\n');
});

test('--expect: упала, но без ожидаемой строки — ERROR; с ней — KILLED', async () => {
  const s = sandbox('d.txt', 'v = 1\n');
  // Падает только на подсадке (база зелёная) и печатает строку go test про чужой тест.
  const cmd = [
    NODE,
    '-e',
    `if(require('fs').readFileSync(${JSON.stringify(s.file)},'utf8').includes('2')){console.log('--- FAIL: TestOther (0.00s)');process.exit(1)}`,
  ];
  const bad = await runCli(
    ['--file', s.file, '--find', '1', '--replace', '2', '--expect', 'TestGuard', '--', ...cmd],
    s.env,
  );
  assert.match(bad.stdout, /^ERROR .*TestGuard/m);
  assert.equal(bad.code, 1);
  const good = await runCli(
    ['--file', s.file, '--find', '1', '--replace', '2', '--expect', 'TestOther', '--', ...cmd],
    s.env,
  );
  assert.match(good.stdout, /^KILLED /m);
  assert.equal(good.code, 0);
  assert.equal(readFileSync(s.file, 'utf8'), 'v = 1\n');
});

test('команда не найдена — ERROR, файл восстановлен', async () => {
  const s = sandbox('e.txt', 'v = 1\n');
  const r = await runCli(
    [
      '--file',
      s.file,
      '--find',
      '1',
      '--replace',
      '2',
      '--no-baseline',
      '--',
      'no-such-cmd-wf-plant-xyz',
    ],
    s.env,
  );
  assert.match(r.stdout, /^ERROR /m);
  assert.equal(r.code, 1);
  assert.equal(readFileSync(s.file, 'utf8'), 'v = 1\n');
  assert.deepEqual(leftovers(s.plantDir), []);
});

test(
  'таймаут убивает дерево процессов (внук тоже), файл восстановлен',
  { timeout: 30_000 },
  async () => {
    const s = sandbox('f.txt', 'спит = нет\n');
    const pidFile = join(s.dir, 'grandchild.pid');
    // Сын запускает внука; внук пишет свой pid и спит вечно; сын тоже спит. На Windows внук — detached: так его
    // не добьёт job-объект сына (node), как не добьёт и тестовый бинарник, запущенный `go test`; на POSIX внук
    // остаётся в группе процессов сына, как у go test.
    const grandchild = `require('fs').writeFileSync(${JSON.stringify(pidFile)},String(process.pid));setInterval(()=>{},1e6)`;
    const son = `require('child_process').spawn(process.execPath,['-e',${JSON.stringify(grandchild)}],{stdio:'inherit',detached:process.platform==='win32'});setInterval(()=>{},1e6)`;
    // В этом же процессе, а не через CLI: на Windows libuv держит детей в job-объекте и добивает их при выходе
    // родителя — снаружи CLI внук умер бы и без убийства дерева. Здесь родитель (тест) жив, проверка честная.
    let out = '';
    const prev = process.env.WF_PLANT_DIR;
    process.env.WF_PLANT_DIR = s.plantDir;
    let code;
    try {
      code = await main(
        [
          '--file',
          s.file,
          '--find',
          'нет',
          '--replace',
          'да',
          '--timeout',
          '3',
          '--no-baseline',
          '--',
          NODE,
          '-e',
          son,
        ],
        {
          out: (t) => (out += t),
          err: () => {},
          signals: new EventEmitter(),
        },
      );
    } finally {
      if (prev === undefined) delete process.env.WF_PLANT_DIR;
      else process.env.WF_PLANT_DIR = prev;
    }
    assert.match(out, /^TIMEOUT /m);
    assert.equal(code, 1);
    assert.equal(readFileSync(s.file, 'utf8'), 'спит = нет\n');
    const pid = Number(readFileSync(pidFile, 'utf8'));
    const dead = await waitDead(pid);
    if (!dead) process.kill(pid);
    assert.ok(dead, `внук ${pid} жив после таймаута`);
  },
);

test(
  'SIGINT посреди команды: дерево убито, файл восстановлен, код 130',
  { timeout: 30_000 },
  async () => {
    const s = sandbox('g.txt', Buffer.concat([BOM, Buffer.from('флаг = истина\r\n')]));
    const before = readFileSync(s.file);
    const signals = new EventEmitter();
    let out = '';
    let childPid = 0;
    const prev = process.env.WF_PLANT_DIR;
    process.env.WF_PLANT_DIR = s.plantDir;
    try {
      const code = await main(
        [
          '--file',
          s.file,
          '--find',
          'истина',
          '--replace',
          'ложь',
          '--no-baseline',
          '--',
          NODE,
          '-e',
          'setInterval(()=>{},1e6)',
        ],
        {
          out: (t) => (out += t),
          err: () => {},
          signals,
          onSpawn: (child) => {
            childPid = child.pid;
            signals.emit('SIGINT', 'SIGINT');
          },
        },
      );
      assert.equal(code, 130);
    } finally {
      if (prev === undefined) delete process.env.WF_PLANT_DIR;
      else process.env.WF_PLANT_DIR = prev;
    }
    assert.match(out, /^ERROR .*SIGINT/m);
    assert.equal(sha(readFileSync(s.file)), sha(before));
    assert.ok(await waitDead(childPid), 'команда пережила SIGINT');
    assert.equal(signals.listenerCount('SIGINT'), 0, 'обработчики сигналов сняты');
  },
);

test('сверка SHA: восстановление записало не то — громкая ошибка, бэкап сохранён', async () => {
  const s = sandbox('h.txt', 'исходник\n');
  const prev = process.env.WF_PLANT_DIR;
  process.env.WF_PLANT_DIR = s.plantDir;
  let out = '';
  try {
    const code = await main(
      [
        '--file',
        s.file,
        '--find',
        'исходник',
        '--replace',
        'MUTANT',
        '--',
        ...failsIfPlanted(s.file),
      ],
      {
        out: (t) => (out += t),
        err: () => {},
        signals: new EventEmitter(),
        restoreWrite: (f) => writeFileSync(f, 'испорчено при записи\n'),
      },
    );
    assert.equal(code, 1);
  } finally {
    if (prev === undefined) delete process.env.WF_PLANT_DIR;
    else process.env.WF_PLANT_DIR = prev;
  }
  assert.match(out, /^ERROR .*SHA-256.*бэкап/m);
  const baks = leftovers(s.plantDir).filter((n) => n.endsWith('.bak'));
  assert.equal(baks.length, 1, 'бэкап оставлен');
  assert.equal(readFileSync(join(s.plantDir, baks[0]), 'utf8'), 'исходник\n');
});

// Жёсткое убийство (таймаут Bash-инструмента, taskkill /F) не даёт выполнить finally: остаются манифест и бэкап
// с мёртвым pid. Такой след собирается вручную — так тест детерминирован.
function crashTrace(s, { original, planted, current, pid, backup = original }) {
  mkdirSync(s.plantDir, { recursive: true });
  writeFileSync(s.file, current);
  const key = lockKey(s.file);
  const bak = join(s.plantDir, `${key}-${pid}-x.bak`);
  writeFileSync(bak, backup);
  writeFileSync(
    join(s.plantDir, `${key}.json`),
    JSON.stringify({
      file: s.file,
      pid,
      originalSha: sha(original),
      plantedSha: sha(planted),
      backup: bak,
      label: 'упавший',
    }),
  );
}
const deadPid = () => spawnSync(NODE, ['-e', '']).pid;

test('восстановление после жёсткого убийства: --recover возвращает файл и убирает след', async () => {
  const original = Buffer.from('исходник\r\n');
  const planted = Buffer.from('MUTANT\r\n');
  const s = sandbox('i.txt', original);
  crashTrace(s, { original, planted, current: planted, pid: deadPid() });
  const r = await runCli(['--recover'], s.env);
  assert.equal(r.code, 0);
  assert.match(r.stderr, /восстановлен/);
  assert.deepEqual(readFileSync(s.file), original);
  assert.deepEqual(leftovers(s.plantDir), []);
});

test('восстановление после жёсткого убийства: следующая подсадка того же файла сначала восстанавливает', async () => {
  const original = Buffer.from('a = 1\n');
  const s = sandbox('j.txt', original);
  crashTrace(s, {
    original,
    planted: Buffer.from('a = 2\n'),
    current: Buffer.from('a = 2\n'),
    pid: deadPid(),
  });
  // Без восстановления find «1» не найдётся: в файле лежит прошлая подсадка.
  const r = await runCli(
    ['--file', s.file, '--find', '1', '--replace', 'MUTANT', '--', ...failsIfPlanted(s.file)],
    s.env,
  );
  assert.match(r.stdout, /^KILLED /m);
  assert.deepEqual(readFileSync(s.file), original);
  assert.deepEqual(leftovers(s.plantDir), []);
});

test('--recover: файл изменён после падения — не трогать, ERROR с путём бэкапа', async () => {
  const original = Buffer.from('исходник\n');
  const s = sandbox('k.txt', original);
  crashTrace(s, {
    original,
    planted: Buffer.from('MUTANT\n'),
    current: 'правка человека\n',
    pid: deadPid(),
  });
  const r = await runCli(['--recover'], s.env);
  assert.equal(r.code, 1);
  assert.match(r.stderr, /ERROR .*изменён.*\.bak/);
  assert.equal(readFileSync(s.file, 'utf8'), 'правка человека\n');
  assert.equal(leftovers(s.plantDir).length, 2, 'след сохранён');
});

test('--recover: бэкап испорчен (SHA не исходный) — файл не трогать', async () => {
  const original = Buffer.from('исходник\n');
  const planted = Buffer.from('MUTANT\n');
  const s = sandbox('l.txt', original);
  crashTrace(s, { original, planted, current: planted, pid: deadPid(), backup: 'мусор\n' });
  const r = await runCli(['--recover'], s.env);
  assert.equal(r.code, 1);
  assert.match(r.stderr, /ERROR .*бэкап/);
  assert.deepEqual(readFileSync(s.file), planted);
});

test('тот же файл занят живым процессом — ERROR «занят», файл не тронут', async () => {
  const original = Buffer.from('a = 1\n');
  const planted = Buffer.from('a = 2\n');
  const s = sandbox('m.txt', original);
  crashTrace(s, { original, planted, current: planted, pid: process.pid });
  const r = await runCli(
    ['--file', s.file, '--find', '2', '--replace', '3', '--', ...failsIfPlanted(s.file, '3')],
    s.env,
  );
  assert.match(r.stdout, /^ERROR .*занят/m);
  assert.equal(r.code, 1);
  assert.deepEqual(readFileSync(s.file), planted, 'чужая подсадка не тронута');
});

test(
  'параллельно на разных файлах с общим каталогом бэкапов — не сталкиваются',
  { timeout: 30_000 },
  async () => {
    const shared = join(TMP, `par${++seq}`);
    const a = sandbox('a.txt', 'первый = 1\n');
    const b = sandbox('b.txt', 'второй = 1\n');
    const env = { WF_PLANT_DIR: shared };
    // Барьер: каждая команда на подсадке отмечается и ждёт отметки соседа — подсадки гарантированно
    // пересекаются во времени. Базовый прогон (без подсадки) проходит сразу.
    const barrier = (me, peer, file) => [
      NODE,
      '-e',
      `const fs=require('fs');if(!fs.readFileSync(${JSON.stringify(file)},'utf8').includes('MUTANT'))process.exit(0);` +
        `fs.writeFileSync(${JSON.stringify(me)},'1');` +
        `const t=setInterval(()=>{if(fs.existsSync(${JSON.stringify(peer)})){clearInterval(t);` +
        `process.exit(fs.readFileSync(${JSON.stringify(file)},'utf8').includes('MUTANT')?1:0)}},20)`,
    ];
    const ma = join(TMP, `par${seq}-a`);
    const mb = join(TMP, `par${seq}-b`);
    const [ra, rb] = await Promise.all([
      runCli(
        [
          '--file',
          a.file,
          '--find',
          '1',
          '--replace',
          'MUTANT',
          '--label',
          'A',
          '--',
          ...barrier(ma, mb, a.file),
        ],
        env,
      ),
      runCli(
        [
          '--file',
          b.file,
          '--find',
          '1',
          '--replace',
          'MUTANT',
          '--label',
          'B',
          '--',
          ...barrier(mb, ma, b.file),
        ],
        env,
      ),
    ]);
    assert.match(ra.stdout, /^KILLED A /m);
    assert.match(rb.stdout, /^KILLED B /m);
    assert.equal(readFileSync(a.file, 'utf8'), 'первый = 1\n');
    assert.equal(readFileSync(b.file, 'utf8'), 'второй = 1\n');
    assert.deepEqual(leftovers(shared), []);
  },
);

test('--spec: две подсадки (одна многострочная) — таблица markdown, код по худшему итогу', async () => {
  const a = sandbox('a.go', 'if x {\n\treturn 1\n}\n');
  const b = sandbox('b.go', 'y := 2\n');
  const spec = join(a.dir, 'spec.json');
  writeFileSync(
    spec,
    JSON.stringify([
      {
        label: 'много-строк',
        file: a.file,
        find: 'if x {\n\treturn 1',
        replace: 'if !x {\n\treturn MUTANT',
        cmd: failsIfPlanted(a.file),
      },
      { label: 'вакуумный', file: b.file, find: '2', replace: '3', cmd: failsIfPlanted(b.file) },
    ]),
  );
  const r = await runCli(['--spec', spec], a.env);
  assert.equal(r.code, 1);
  assert.match(r.stdout, /^\| label \| файл \| итог \| время \|$/m);
  assert.match(r.stdout, /^\| много-строк \| .*a\.go \| KILLED \(exit 1\) \| \d+\.\d с \|$/m);
  assert.match(r.stdout, /^\| вакуумный \| .*b\.go \| SURVIVED \| \d+\.\d с \|$/m);
  assert.equal(readFileSync(a.file, 'utf8'), 'if x {\n\treturn 1\n}\n');
  assert.equal(readFileSync(b.file, 'utf8'), 'y := 2\n');
});

// regen имитирует sqlc: копирует источник в «сгенерированный» файл.
test('regen: после возврата сгенерированное совпало; недетерминированный regen — ERROR', async () => {
  const s = sandbox('q.sql', 'SELECT 1;\n');
  const gen = join(s.dir, 'gen');
  mkdirSync(gen);
  const out = join(gen, 'q.go');
  writeFileSync(out, '// gen: SELECT 1;\n');
  const copy = [
    NODE,
    '-e',
    `const fs=require('fs');fs.writeFileSync(${JSON.stringify(out)},'// gen: '+fs.readFileSync(${JSON.stringify(s.file)},'utf8'))`,
  ];
  const spec = join(s.dir, 'spec.json');
  writeFileSync(
    spec,
    JSON.stringify([
      {
        label: 'sql',
        file: s.file,
        find: '1',
        replace: 'MUTANT',
        regen: copy,
        gen: [gen],
        cmd: failsIfPlanted(out),
      },
    ]),
  );
  const ok = await runCli(['--spec', spec], s.env);
  assert.match(ok.stdout, /\| sql \| .* \| KILLED \(exit 1\) \|/);
  assert.equal(ok.code, 0);
  assert.equal(readFileSync(out, 'utf8'), '// gen: SELECT 1;\n');

  const noisy = [
    NODE,
    '-e',
    `require('fs').writeFileSync(${JSON.stringify(out)},'// gen '+Date.now()+Math.random())`,
  ];
  writeFileSync(
    spec,
    JSON.stringify([
      {
        label: 'sql',
        file: s.file,
        find: '1',
        replace: 'MUTANT',
        regen: noisy,
        gen: [gen],
        baseline: false,
        cmd: [NODE, '-e', 'process.exit(1)'],
      },
    ]),
  );
  const bad = await runCli(['--spec', spec], s.env);
  assert.equal(bad.code, 1);
  assert.match(bad.stdout, /\| sql \| .* \| ERROR/);
  assert.match(bad.stderr, /сгенерирован.*q\.go/);
  assert.equal(readFileSync(s.file, 'utf8'), 'SELECT 1;\n');
});

test(
  'SIGINT после regen: сгенерированное тоже возвращено (regen после возврата идёт и при прерывании)',
  { timeout: 30_000 },
  async () => {
    const s = sandbox('r.sql', 'SELECT 1;\n');
    const gen = join(s.dir, 'gen');
    mkdirSync(gen);
    const out = join(gen, 'r.go');
    writeFileSync(out, '// gen: SELECT 1;\n');
    const copy = [
      NODE,
      '-e',
      `const fs=require('fs');fs.writeFileSync(${JSON.stringify(out)},'// gen: '+fs.readFileSync(${JSON.stringify(s.file)},'utf8'))`,
    ];
    const spec = join(s.dir, 'spec.json');
    writeFileSync(
      spec,
      JSON.stringify([
        {
          label: 'sql',
          file: s.file,
          find: '1',
          replace: 'MUTANT',
          regen: copy,
          gen: [gen],
          baseline: false,
          cmd: [NODE, '-e', 'setInterval(()=>{},1e6)'],
        },
      ]),
    );
    const signals = new EventEmitter();
    let spawns = 0;
    let genDuringCmd = '';
    const prev = process.env.WF_PLANT_DIR;
    process.env.WF_PLANT_DIR = s.plantDir;
    try {
      const code = await main(['--spec', spec], {
        out: () => {},
        err: () => {},
        signals,
        onSpawn: () => {
          // 1-й запуск — regen после подсадки, 2-й — команда: прерываем, когда сгенерированное уже с подсадкой.
          if (++spawns === 2) {
            genDuringCmd = readFileSync(out, 'utf8');
            signals.emit('SIGINT', 'SIGINT');
          }
        },
      });
      assert.equal(code, 130);
    } finally {
      if (prev === undefined) delete process.env.WF_PLANT_DIR;
      else process.env.WF_PLANT_DIR = prev;
    }
    assert.equal(
      genDuringCmd,
      '// gen: SELECT MUTANT;\n',
      'фикстура: во время команды сгенерированное с подсадкой',
    );
    assert.equal(readFileSync(out, 'utf8'), '// gen: SELECT 1;\n');
    assert.equal(readFileSync(s.file, 'utf8'), 'SELECT 1;\n');
  },
);

// Только Windows: там .cmd (pnpm и т. п.) идёт через cmd.exe, и аргументы с пробелами и кавычками надо экранировать
// самому; на POSIX .cmd не исполняемый, проверять нечего.
test(
  'Windows: .cmd получает аргументы с пробелом и кавычкой без порчи',
  { skip: process.platform !== 'win32' },
  async () => {
    const s = sandbox('w.txt', 'v = 1\n');
    writeFileSync(
      join(s.dir, 'fake.cmd'),
      `@"${NODE}" -e "process.exit(process.argv[1]==='a b' && process.argv[2]==='x\\"y' ? 1 : 0)" %*\r\n`,
    );
    const r = await runCli(
      [
        '--file',
        s.file,
        '--find',
        '1',
        '--replace',
        '2',
        '--cwd',
        s.dir,
        '--no-baseline',
        '--',
        './fake.cmd',
        'a b',
        'x"y',
      ],
      s.env,
    );
    assert.match(r.stdout, /^KILLED /m, r.stdout + r.stderr);
  },
);

test('--recover: замок без подсадки (умер между замком и записью) — снять, файл не трогать', async () => {
  const s = sandbox('n.txt', 'как было\n');
  mkdirSync(s.plantDir, { recursive: true });
  writeFileSync(
    join(s.plantDir, `${lockKey(s.file)}.json`),
    JSON.stringify({ file: s.file, pid: deadPid(), label: 'полузамок' }),
  );
  const r = await runCli(['--recover'], s.env);
  assert.equal(r.code, 0);
  assert.match(r.stderr, /замок без подсадки снят/);
  assert.equal(readFileSync(s.file, 'utf8'), 'как было\n');
  assert.deepEqual(leftovers(s.plantDir), []);
});

// --- раунд 1 ---

// main в этом же процессе со своим каталогом следов.
async function inProcess(plantDir, argv, io = {}) {
  const prev = process.env.WF_PLANT_DIR;
  process.env.WF_PLANT_DIR = plantDir;
  let out = '';
  let err = '';
  try {
    const code = await main(argv, {
      out: (t) => (out += t),
      err: (t) => (err += t),
      signals: new EventEmitter(),
      ...io,
    });
    return { code, out, err };
  } finally {
    if (prev === undefined) delete process.env.WF_PLANT_DIR;
    else process.env.WF_PLANT_DIR = prev;
  }
}

// Команда-счётчик: дописывает строку в файл при каждом запуске, падает на подсадке в любом из файлов.
const counted = (counter, files, alwaysFail = false) => [
  NODE,
  '-e',
  `const fs=require('fs');fs.appendFileSync(${JSON.stringify(counter)},'x\\n');` +
    (alwaysFail
      ? 'process.exit(1)'
      : `process.exit(${JSON.stringify(files)}.some(f=>fs.readFileSync(f,'utf8').includes('MUTANT'))?1:0)`),
];
const runs = (counter) =>
  existsSync(counter) ? readFileSync(counter, 'utf8').split('\n').filter(Boolean).length : 0;

test('I1 база красная: ни одной подсадки, ERROR «без подсадки», команда запущена один раз', async () => {
  const s = sandbox('base.txt', 'v = 1\n');
  const counter = join(s.dir, 'runs');
  const r = await runCli(
    ['--file', s.file, '--find', '1', '--replace', 'MUTANT', '--', ...counted(counter, [], true)],
    s.env,
  );
  assert.match(r.stdout, /^ERROR .*красная без подсадки/m);
  assert.equal(r.code, 1);
  assert.equal(runs(counter), 1, 'только базовый прогон');
  assert.equal(readFileSync(s.file, 'utf8'), 'v = 1\n');
  assert.deepEqual(leftovers(s.plantDir), []);
});

test('I1 база в --spec — один раз на команду+cwd, потом подсадки', async () => {
  const a = sandbox('a.txt', 'a = 1\n');
  const b = sandbox('b.txt', 'b = 1\n');
  const counter = join(a.dir, 'runs');
  const cmd = counted(counter, [a.file, b.file]);
  const spec = join(a.dir, 'spec.json');
  writeFileSync(
    spec,
    JSON.stringify({
      defaults: { cmd, replace: 'MUTANT', find: '1' },
      plants: [{ file: a.file }, { file: b.file }],
    }),
  );
  const r = await runCli(['--spec', spec], a.env);
  assert.equal(r.code, 0, r.stdout + r.stderr);
  assert.equal(runs(counter), 3, '1 база + 2 подсадки');
});

test('I2 failedTests/expectMatches: границы имени по формату go test, vitest, node:test', () => {
  const goV = [
    '=== RUN   TestGuard',
    '--- PASS: TestGuard (0.00s)',
    '=== RUN   TestCreateDuplicate',
    '    --- FAIL: TestCreateDuplicate/same_email (0.01s)',
    '--- FAIL: TestCreateDuplicate (0.01s)',
    'FAIL',
  ].join('\n');
  assert.deepEqual(failedTests(goV), ['TestCreateDuplicate/same_email', 'TestCreateDuplicate']);
  assert.equal(expectMatches(goV, 'TestGuard'), false, '=== RUN и --- PASS не засчитываются');
  assert.equal(expectMatches(goV, 'TestCreate'), false, 'TestCreate ⊄ TestCreateDuplicate');
  assert.equal(expectMatches(goV, 'TestCreateDuplicate'), true);
  assert.equal(expectMatches(goV, 'TestCreateDuplicate/same_email'), true);
  const timeout = 'panic: test timed out after 5m0s\n\trunning tests:\n\t\tTestGuard (5m0s)\n';
  assert.equal(expectMatches(timeout, 'TestGuard'), false, 'паника таймаута — не падение теста');

  const vitest = [
    ' \x1b[31m❯\x1b[39m src/form.test.tsx (3 tests | 1 failed) 20ms',
    '   \x1b[31m×\x1b[39m Form > shows error 5ms',
    ' FAIL  src/form.test.tsx > Form > shows error',
  ].join('\n');
  assert.equal(expectMatches(vitest, 'shows error'), true);
  assert.equal(expectMatches(vitest, 'Form > shows error'), true);
  assert.equal(expectMatches(vitest, 'shows'), false);
  assert.equal(expectMatches(vitest, 'error'), false);

  assert.equal(
    expectMatches('✖ возврат при исключении (12.5ms)\n', 'возврат при исключении'),
    true,
  );
  assert.equal(
    expectMatches('✔ возврат при исключении (12.5ms)\n', 'возврат при исключении'),
    false,
  );
  assert.equal(expectMatches('not ok 3 - возврат # SKIP\n', 'возврат'), true);
});

test('I2 --expect: упал другой тест с тем же префиксом, у стража -v-вывод — ERROR, не KILLED', async () => {
  const s = sandbox('e2.txt', 'v = 1\n');
  const lines = [
    '=== RUN   TestCreate',
    '--- PASS: TestCreate (0.00s)',
    '--- FAIL: TestCreateDuplicate (0.00s)',
  ];
  const cmd = [
    NODE,
    '-e',
    `if(require('fs').readFileSync(${JSON.stringify(s.file)},'utf8').includes('MUTANT')){console.log(${JSON.stringify(lines.join('\n'))});process.exit(1)}`,
  ];
  const r = await runCli(
    [
      '--file',
      s.file,
      '--find',
      '1',
      '--replace',
      'MUTANT',
      '--expect',
      'TestCreate',
      '--',
      ...cmd,
    ],
    s.env,
  );
  assert.match(r.stdout, /^ERROR .*TestCreate.*нет среди упавших.*TestCreateDuplicate/m);
  assert.equal(r.code, 1);
});

test('I3 --recover: живой pid со свежим пульсом назван, код 1; старый пульс или --force — возврат', async () => {
  const original = Buffer.from('исходник\n');
  const planted = Buffer.from('MUTANT\n');
  const s = sandbox('live.txt', original);
  crashTrace(s, { original, planted, current: planted, pid: process.pid });
  const live = await runCli(['--recover'], s.env);
  assert.equal(live.code, 1);
  assert.match(live.stderr, /занят живым pid \d+, файл .*live\.txt — с подсадкой/);
  assert.deepEqual(readFileSync(s.file), planted, 'живой след не тронут');

  // Пульс старше порога: pid переиспользован чужим процессом — владелец мёртв.
  const old = new Date(Date.now() - STALE_MS - 5000);
  utimesSync(join(s.plantDir, `${lockKey(s.file)}.json`), old, old);
  const stale = await runCli(['--recover'], s.env);
  assert.equal(stale.code, 0, stale.stderr);
  assert.deepEqual(readFileSync(s.file), original);

  crashTrace(s, { original, planted, current: planted, pid: process.pid });
  const forced = await runCli(['--recover', '--force'], s.env);
  assert.equal(forced.code, 0, forced.stderr);
  assert.deepEqual(readFileSync(s.file), original);
  assert.deepEqual(leftovers(s.plantDir), []);
});

test(
  'I3 пульс: владелец обновляет mtime манифеста, пока идёт команда',
  { timeout: 30_000 },
  async () => {
    const s = sandbox('beat.txt', 'v = 1\n');
    const manifest = join(s.plantDir, `${lockKey(s.file)}.json`);
    // Без настоящего времени: пульс — 100 мс, команда сама следит за mtime манифеста и падает (подсадка
    // «убита», код 0) на втором его изменении — после последней записи манифеста до запуска команды его
    // меняет только пульс. Пульса нет — через 20 с выходит с 0 (SURVIVED, код 1). Прежний тест сравнивал
    // mtime с порогом 4 с при пульсе 5 с: под нагрузкой задержка между записями манифеста съедала запас.
    const watch =
      `const fs=require('fs'),m=${JSON.stringify(manifest)};let base=fs.statSync(m).mtimeMs,seen=0;` +
      `setInterval(()=>{let x;try{x=fs.statSync(m).mtimeMs}catch{return}` +
      `if(x!==base){base=x;if(++seen>=2)process.exit(1)}},20);setTimeout(()=>process.exit(0),20000)`;
    const r = await inProcess(
      s.plantDir,
      ['--file', s.file, '--find', '1', '--replace', '2', '--no-baseline', '--', NODE, '-e', watch],
      { heartbeatMs: 100 },
    );
    assert.equal(r.code, 0, `пульса нет — команда не увидела смены mtime: ${r.out}`);
    assert.match(r.out, /KILLED/);
  },
);

test('I4 исключение посреди подсадки: файл возвращён байт в байт, ERROR, след убран', async () => {
  const content = Buffer.concat([BOM, Buffer.from('сумма = 1\r\n')]);
  const s = sandbox('throw.txt', content);
  const r = await inProcess(
    s.plantDir,
    ['--file', s.file, '--find', '1', '--replace', 'MUTANT', '--', ...failsIfPlanted(s.file)],
    {
      plantWrite: (f, data) => {
        writeFileSync(f, data);
        throw new Error('сбой посреди записи');
      },
    },
  );
  assert.equal(r.code, 1);
  assert.match(r.out, /^ERROR .*исключение.*сбой посреди записи/m);
  assert.equal(sha(readFileSync(s.file)), sha(content));
  assert.deepEqual(leftovers(s.plantDir), []);
});

test('I5 бюджет: подсадка, которой не хватит остатка, не начинается — NOT RUN, код 1', async () => {
  const a = sandbox('a.txt', 'a = 1\n');
  const b = sandbox('b.txt', 'b = 1\n');
  const counter = join(a.dir, 'runs');
  const spec = join(a.dir, 'spec.json');
  writeFileSync(
    spec,
    JSON.stringify({
      defaults: {
        find: '1',
        replace: 'MUTANT',
        baseline: false,
        cmd: counted(counter, [a.file, b.file]),
      },
      plants: [
        { label: 'влезает', file: a.file, timeout: 1 },
        { label: 'не влезает', file: b.file, timeout: 10 },
      ],
    }),
  );
  const r = await runCli(['--spec', spec, '--budget', '5'], a.env);
  assert.equal(r.code, 1);
  assert.match(r.stdout, /^\| влезает \| .* \| KILLED \(exit 1\) \|/m);
  assert.match(r.stdout, /^\| не влезает \| .* \| NOT RUN \|/m);
  assert.match(r.stderr, /^NOT RUN не влезает \(бюджет: осталось \d+ с, нужно до 10 с/m);
  assert.equal(runs(counter), 1, 'вторая команда не запускалась');
  assert.equal(readFileSync(b.file, 'utf8'), 'b = 1\n');
});

test('m7 файл менялся во время команды: ERROR, изменённое сохранено, исходник возвращён', async () => {
  const s = sandbox('chg.txt', 'v = 1\n');
  const cmd = [
    NODE,
    '-e',
    `const fs=require('fs');const t=fs.readFileSync(${JSON.stringify(s.file)},'utf8');` +
      `if(t.includes('MUTANT')){fs.writeFileSync(${JSON.stringify(s.file)},'чужая правка\\n');process.exit(1)}`,
  ];
  const r = await runCli(
    ['--file', s.file, '--find', '1', '--replace', 'MUTANT', '--', ...cmd],
    s.env,
  );
  assert.match(r.stdout, /^ERROR .*менялся во время команды.*\.changed/m);
  assert.equal(r.code, 1);
  assert.equal(readFileSync(s.file, 'utf8'), 'v = 1\n');
  const changed = leftovers(s.plantDir).filter((n) => n.endsWith('.changed'));
  assert.equal(changed.length, 1);
  assert.equal(readFileSync(join(s.plantDir, changed[0]), 'utf8'), 'чужая правка\n');
});

// --- раунд 2 ---

// База спит 1 с (без подсадки, exit 0); на подсадке команда сразу падает.
const slowBase = (counter, file) => [
  NODE,
  '-e',
  `const fs=require('fs');fs.appendFileSync(${JSON.stringify(counter)},'x\\n');` +
    `if(fs.readFileSync(${JSON.stringify(file)},'utf8').includes('MUTANT'))process.exit(1);` +
    'setTimeout(()=>process.exit(0),1000)',
];

test('R2 бюджет: база вычитается по факту — --timeout 300 при бюджете 540 запускает подсадку', async () => {
  const s = sandbox('db.txt', 'v = 1\n');
  const counter = join(s.dir, 'runs');
  const r = await runCli(
    [
      '--file',
      s.file,
      '--find',
      '1',
      '--replace',
      'MUTANT',
      '--timeout',
      '300',
      '--budget',
      '540',
      '--',
      ...slowBase(counter, s.file),
    ],
    s.env,
  );
  assert.match(r.stdout, /^KILLED /m, r.stdout);
  assert.equal(r.code, 0);
  assert.equal(runs(counter), 2, 'база + подсадка');
});

test('R2 бюджет перепроверяется после базы: остатка не хватило — NOT RUN, файл не тронут', async () => {
  const s = sandbox('db2.txt', 'v = 1\n');
  const counter = join(s.dir, 'runs');
  // До базы 2 ≤ 2.5 — пускаем; после базы (~1 с) остаток < 2 — подсадки нет.
  const r = await runCli(
    [
      '--file',
      s.file,
      '--find',
      '1',
      '--replace',
      'MUTANT',
      '--timeout',
      '2',
      '--budget',
      '2.5',
      '--',
      ...slowBase(counter, s.file),
    ],
    s.env,
  );
  assert.match(r.stdout, /^NOT RUN .*нужно до 2 с — уменьшите --timeout/m, r.stdout);
  assert.doesNotMatch(r.stdout, /разбейте серию/, 'одна подсадка — не «серия»');
  assert.equal(r.code, 1);
  assert.equal(runs(counter), 1, 'только база');
  assert.equal(readFileSync(s.file, 'utf8'), 'v = 1\n');
  assert.deepEqual(leftovers(s.plantDir), []);
});

test('R2 сбой записи .changed не мешает возврату исходника', async () => {
  const s = sandbox('chg2.txt', 'v = 1\n');
  const cmd = [
    NODE,
    '-e',
    `const fs=require('fs');if(fs.readFileSync(${JSON.stringify(s.file)},'utf8').includes('MUTANT')){` +
      `fs.writeFileSync(${JSON.stringify(s.file)},'чужая правка\\n');process.exit(1)}`,
  ];
  const r = await inProcess(
    s.plantDir,
    ['--file', s.file, '--find', '1', '--replace', 'MUTANT', '--', ...cmd],
    {
      changedWrite: () => {
        throw new Error('диск полон');
      },
    },
  );
  assert.equal(r.code, 1);
  assert.match(r.out, /^ERROR .*менялся во время команды.*сохранить не удалось: диск полон/m);
  assert.equal(readFileSync(s.file, 'utf8'), 'v = 1\n');
  assert.deepEqual(leftovers(s.plantDir), []);
});
