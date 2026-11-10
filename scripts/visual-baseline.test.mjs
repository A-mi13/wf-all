import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { test } from 'node:test';
import {
  artifactAlive,
  changed,
  collectResults,
  findBase,
  findBaseline,
  playwrightImage,
  prOfCommit,
  resolveBaseline,
  summarize,
  VISUAL_PATHS,
  visualMode,
} from './visual-baseline.mjs';

// Поиск эталона визуального сравнения (спека UI-фундамента §5.3, план UF3.5): исполнитель команд подменён —
// git и gh отвечают по сценарию теста; неописанная команда — ошибка теста.

function runner(script) {
  const calls = [];
  const run = (cmd, args) => {
    const line = [cmd, ...args].join(' ');
    calls.push(line);
    for (const [match, result] of script) {
      if (typeof match === 'string' ? line === match : match.test(line)) {
        return typeof result === 'function' ? result(line) : result;
      }
    }
    throw new Error(`неописанная команда: ${line}`);
  };
  return { run, calls };
}
const ok = (stdout = '') => ({ code: 0, stdout });
const no = (code = 1) => ({ code, stdout: '' });
const PATHS = VISUAL_PATHS.join(' ');

test('база — родитель merge-коммита HEAD^1, а не base.sha события', () => {
  const { run } = runner([['git rev-parse HEAD^1', ok('b'.repeat(40) + '\n')]]);
  assert.equal(findBase(run), 'b'.repeat(40));
});

test('HEAD^1 нет (неглубокий checkout) — ошибка с подсказкой, а не пустая база', () => {
  const { run } = runner([['git rev-parse HEAD^1', no(128)]]);
  assert.throws(() => findBase(run), /fetch-depth: 0/);
});

test('нет визуальных изменений → changed=false; есть → true', () => {
  const empty = runner([[`git diff --name-only BASE HEAD -- ${PATHS}`, ok('\n')]]);
  assert.equal(changed(empty.run, 'BASE'), false);
  const some = runner([
    [`git diff --name-only BASE HEAD -- ${PATHS}`, ok('apps/admin/src/ui/Button.tsx\n')],
  ]);
  assert.equal(changed(some.run, 'BASE'), true);
});

test('визуальные пути: исходники админки без тестов, токены, иконки, lock, сам workflow и конфиги снимков', () => {
  for (const p of [
    'apps/admin/src',
    ':(exclude,glob)apps/admin/src/**/*.test.*',
    'apps/admin/index.html',
    'apps/admin/public',
    'apps/admin/vite.config.ts',
    'apps/admin/vite.shots.ts',
    'apps/admin/package.json',
    'apps/admin/.ladle',
    'apps/admin/visual',
    'packages/tokens',
    'packages/icons',
    'contracts/tokens',
    'pnpm-lock.yaml',
    '.github/workflows/admin-visual.yml',
    'scripts/visual-baseline.mjs',
  ]) {
    assert.ok(VISUAL_PATHS.includes(p), p);
  }
});

const LIST =
  'gh run list --workflow admin-visual.yml --branch main --event push --status success --json databaseId,headSha --limit 100';
const runs = (...r) =>
  ok(JSON.stringify(r.map(([databaseId, headSha]) => ({ databaseId, headSha }))));

test('эталон — первый предок базы без изменений визуальных путей между ними (второй в списке)', () => {
  const { run, calls } = runner([
    [LIST, runs([30, 'c3'], [20, 'c2'], [10, 'c1'])],
    ['git merge-base --is-ancestor c3 BASE', no(1)], // прогон по коммиту не из истории базы
    ['git merge-base --is-ancestor c2 BASE', ok()],
    [`git diff --quiet c2 BASE -- ${PATHS}`, ok()],
  ]);
  assert.deepEqual(findBaseline(run, 'BASE'), { runId: '20', sha: 'c2' });
  assert.ok(!calls.some((c) => c.includes(' c1 ')), 'дальше первого подходящего не ищет');
});

test('предок, но вид менялся между ним и базой — не эталон, ищем дальше', () => {
  const { run } = runner([
    [LIST, runs([20, 'c2'], [10, 'c1'])],
    ['git merge-base --is-ancestor c2 BASE', ok()],
    [`git diff --quiet c2 BASE -- ${PATHS}`, no(1)],
    ['git merge-base --is-ancestor c1 BASE', ok()],
    [`git diff --quiet c1 BASE -- ${PATHS}`, no(1)],
  ]);
  assert.equal(findBaseline(run, 'BASE'), null);
});

test('коммит прогона неизвестен истории (128) — пропуск, не падение', () => {
  const { run } = runner([
    [LIST, runs([20, 'gone'], [10, 'c1'])],
    ['git merge-base --is-ancestor gone BASE', no(128)],
    ['git merge-base --is-ancestor c1 BASE', ok()],
    [`git diff --quiet c1 BASE -- ${PATHS}`, ok()],
  ]);
  assert.deepEqual(findBaseline(run, 'BASE'), { runId: '10', sha: 'c1' });
});

test('успешных прогонов main нет — эталона нет', () => {
  const { run } = runner([[LIST, ok('[]')]]);
  assert.equal(findBaseline(run, 'BASE'), null);
});

test('gh не ответил — ошибка, а не «эталона нет» (иначе регрессия прошла бы молча)', () => {
  const { run } = runner([[LIST, no(1)]]);
  assert.throws(() => findBaseline(run, 'BASE'), /gh run list/);
});

const ARTS = 'gh api repos/o/r/actions/runs/20/artifacts';
test('артефакт эталона: живой — имя; expired — пусто; чужие имена не в счёт', () => {
  const alive = runner([
    [
      ARTS,
      ok(
        JSON.stringify({
          artifacts: [
            { name: 'report', expired: false },
            { name: 'visual-baseline-c2', expired: false },
          ],
        }),
      ),
    ],
  ]);
  assert.equal(artifactAlive(alive.run, 'o/r', '20'), 'visual-baseline-c2');
  const expired = runner([
    [ARTS, ok(JSON.stringify({ artifacts: [{ name: 'visual-baseline-c2', expired: true }] }))],
  ]);
  assert.equal(artifactAlive(expired.run, 'o/r', '20'), null);
  const none = runner([
    [ARTS, ok(JSON.stringify({ artifacts: [{ name: 'report', expired: false }] }))],
  ]);
  assert.equal(artifactAlive(none.run, 'o/r', '20'), null);
});

test('эталон с истёкшим артефактом — ищем следующий подходящий', () => {
  const { run } = runner([
    [LIST, runs([20, 'c2'], [10, 'c1'])],
    ['git merge-base --is-ancestor c2 BASE', ok()],
    [`git diff --quiet c2 BASE -- ${PATHS}`, ok()],
    [ARTS, ok(JSON.stringify({ artifacts: [{ name: 'visual-baseline-c2', expired: true }] }))],
    ['git merge-base --is-ancestor c1 BASE', ok()],
    [`git diff --quiet c1 BASE -- ${PATHS}`, ok()],
    [
      'gh api repos/o/r/actions/runs/10/artifacts',
      ok(JSON.stringify({ artifacts: [{ name: 'visual-baseline-c1', expired: false }] })),
    ],
  ]);
  assert.deepEqual(resolveBaseline(run, 'o/r', 'BASE'), {
    runId: '10',
    sha: 'c1',
    artifact: 'visual-baseline-c1',
  });
});

test('PR коммита main: номер и метка visual-change; коммит без PR — пусто', () => {
  const pulls = 'gh api repos/o/r/commits/S/pulls';
  const with_ = runner([
    [
      pulls,
      ok(JSON.stringify([{ number: 42, labels: [{ name: 'docs' }, { name: 'visual-change' }] }])),
    ],
  ]);
  assert.deepEqual(prOfCommit(with_.run, 'o/r', 'S'), { number: 42, visualChange: true });
  const without = runner([
    [pulls, ok(JSON.stringify([{ number: 7, labels: [{ name: 'docs' }] }]))],
  ]);
  assert.deepEqual(prOfCommit(without.run, 'o/r', 'S'), { number: 7, visualChange: false });
  const direct = runner([[pulls, ok('[]')]]);
  assert.equal(prOfCommit(direct.run, 'o/r', 'S'), null);
});

test('образ Playwright — по версии @playwright/test админки', () => {
  assert.equal(playwrightImage('1.64.1'), 'mcr.microsoft.com/playwright:v1.64.1-noble');
  assert.throws(() => playwrightImage('^1.64.1'), /точная версия/);
});

// Сам workflow: без paths, нужные события, группа concurrency посторонней метки уникальна (m-g), условие метки
// на baseline (m-e), права только на чтение. Текстом — у node:test нет YAML-парсера без зависимостей.
const wf = readFileSync(join(import.meta.dirname, '../.github/workflows/admin-visual.yml'), 'utf8');

test('admin-visual.yml: без paths, события PR с метками, права на чтение', () => {
  assert.doesNotMatch(wf, /^\s*paths:/m);
  assert.match(wf, /types: \[opened, synchronize, reopened, labeled, unlabeled\]/);
  assert.match(wf, /push:\s*\n\s*branches: \[main\]/);
  assert.match(
    wf,
    /permissions:\s*\n\s*contents: read\s*\n\s*actions: read\s*\n\s*pull-requests: read/,
  );
  assert.match(wf, /cancel-in-progress: true/);
});

test('admin-visual.yml: посторонняя метка получает свою группу (+run_id) и не запускает сравнение', () => {
  const group = wf.match(/group: (.+)/)?.[1] ?? '';
  assert.match(group, /admin-visual-/);
  assert.match(group, /github\.event\.pull_request\.number \|\| github\.sha/);
  assert.match(group, /github\.event\.label\.name != 'visual-change'/);
  assert.match(group, /github\.run_id/);
  assert.match(
    wf,
    /github\.event\.action != 'labeled' && github\.event\.action != 'unlabeled'\) \|\| github\.event\.label\.name == 'visual-change'/,
  );
});

test('admin-visual.yml: действия закреплены полным SHA', () => {
  for (const m of wf.matchAll(/uses: (\S+)/g)) {
    assert.match(m[1], /@[0-9a-f]{40}$/, m[1]);
  }
});

// ——— job snapshots: режим и сводка ———
const IMG = 'mcr.microsoft.com/playwright:v1.64.1-noble';

test('режим: эталона нет — сравнения нет; PR без метки с эталоном — строгое', () => {
  assert.deepEqual(visualMode({ event: 'pull_request', labeled: false, meta: null, image: IMG }), {
    compare: false,
    informative: false,
    note: 'Эталона нет: вид не сверен.',
  });
  const strict = visualMode({
    event: 'pull_request',
    labeled: false,
    meta: { image: IMG },
    image: IMG,
  });
  assert.equal(strict.compare, true);
  assert.equal(strict.informative, false);
});

test('режим: информативно — на main, с меткой visual-change, при другом образе эталона', () => {
  const meta = { image: IMG };
  assert.equal(visualMode({ event: 'push', labeled: false, meta, image: IMG }).informative, true);
  assert.equal(
    visualMode({ event: 'pull_request', labeled: true, meta, image: IMG }).informative,
    true,
  );
  const other = visualMode({
    event: 'pull_request',
    labeled: false,
    meta: { image: 'mcr.microsoft.com/playwright:v1.63.0-noble' },
    image: IMG,
  });
  assert.equal(other.informative, true);
  assert.match(other.note, /не совпадает/);
});

const report = {
  suites: [
    {
      specs: [
        {
          tests: [
            {
              status: 'expected',
              annotations: [
                { type: 'visual-diff', description: 'stories/ui--button--default-light-1440.png' },
              ],
            },
          ],
        },
        { tests: [{ status: 'unexpected', annotations: [] }] },
      ],
      suites: [
        {
          specs: [
            {
              tests: [
                {
                  status: 'expected',
                  annotations: [{ type: 'visual-new', description: 'screens/login-dark-390.png' }],
                },
              ],
            },
          ],
        },
      ],
    },
  ],
};

test('отчёт Playwright: отличия, новые снимки и упавшие — и во вложенных наборах', () => {
  assert.deepEqual(collectResults(report), {
    diffs: ['stories/ui--button--default-light-1440.png'],
    missing: ['screens/login-dark-390.png'],
    failed: 1,
  });
});

test('main: отличие у PR без метки visual-change — предупреждение с номером PR; с меткой — нет', () => {
  const results = collectResults(report);
  const mode = { compare: true, informative: true, note: '' };
  const w = summarize({ results, mode, event: 'push', pr: '42', prVisualChange: false }).warnings;
  assert.deepEqual(w, [['Вид изменён без метки', 'PR #42: 1 отличий, отчёт — артефакт прогона']]);
  assert.deepEqual(
    summarize({ results, mode, event: 'push', pr: '42', prVisualChange: true }).warnings,
    [],
  );
  assert.deepEqual(
    summarize({ results, mode, event: 'push', pr: '', prVisualChange: false }).warnings,
    [],
  );
});

test('сводка: эталона нет — предупреждение «вид не сверен»; список отличий в markdown', () => {
  const results = collectResults(report);
  const s = summarize({
    results,
    mode: { compare: false, informative: false, note: 'Эталона нет: вид не сверен.' },
    event: 'pull_request',
    pr: '',
    prVisualChange: false,
  });
  assert.deepEqual(s.warnings, [['Эталона нет', 'Эталона нет: вид не сверен.']]);
  assert.match(s.markdown, /- stories\/ui--button--default-light-1440\.png/);
});
