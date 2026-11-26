// scripts/s3.sh (dev-Garage, план media Task 3): разбор аргументов и поведение без Docker — вместо docker в PATH
// стоит фальшивый скрипт, который пишет свои аргументы в журнал.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { after, before, test } from 'node:test';

const SCRIPT = path.join(import.meta.dirname, 's3.sh');
let dir;
let log;

before(() => {
  dir = mkdtempSync(path.join(tmpdir(), 'wf-s3-'));
  log = path.join(dir, 'docker.log');
  // FAKE_DOCKER_EXISTS=1 — контейнер и том есть (остановлены), иначе их нет
  writeFileSync(
    path.join(dir, 'docker'),
    [
      '#!/usr/bin/env bash',
      'printf "%s\\n" "$*" >> "$FAKE_DOCKER_LOG"',
      'case "$1 $2" in',
      '  "container inspect"|"volume inspect")',
      '    [ "${FAKE_DOCKER_EXISTS:-}" = 1 ] || { echo "Error: No such object" >&2; exit 1; }',
      '    [ "$3" = "-f" ] && echo false ;;',
      'esac',
      'exit 0',
      '',
    ].join('\n'),
    { mode: 0o755 },
  );
});

after(() => rmSync(dir, { recursive: true, force: true }));

function run(args, env = {}) {
  rmSync(log, { force: true });
  const r = spawnSync('bash', [SCRIPT, ...args], {
    encoding: 'utf8',
    env: {
      ...process.env,
      PATH: dir + path.delimiter + process.env.PATH,
      FAKE_DOCKER_LOG: log,
      ...env,
    },
  });
  const calls = existsSync(log) ? readFileSync(log, 'utf8').trim().split('\n') : [];
  return { status: r.status, stdout: r.stdout, stderr: r.stderr, calls };
}

test('без команды, с неизвестной командой и лишним аргументом — подсказка и код ошибки', () => {
  for (const args of [[], ['start'], ['env', 'extra']]) {
    const r = run(args);
    assert.notEqual(r.status, 0, args.join(' '));
    assert.match(r.stderr, /up \| env \| status \| down/, args.join(' '));
    assert.deepEqual(r.calls, [], `docker не вызывался: ${args.join(' ')}`);
  }
});

test('env без запущенного контейнера — понятная ошибка, ничего не выведено и не создано', () => {
  for (const exists of ['', '1']) {
    const r = run(['env'], { FAKE_DOCKER_EXISTS: exists });
    assert.notEqual(r.status, 0);
    assert.match(r.stderr, /wf-s3-dev не запущен — сначала bash scripts\/s3\.sh up/);
    assert.equal(r.stdout, '');
    assert.ok(
      r.calls.every((c) => c.startsWith('container inspect')),
      `только проверка контейнера: ${r.calls}`,
    );
  }
});

test('порт — только число', () => {
  const r = run(['up'], { WF_S3_PORT: '13900;rm' });
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /не число/);
  assert.deepEqual(r.calls, []);
});

test('down удаляет только свой контейнер и том wf-s3-dev', () => {
  const r = run(['down'], { FAKE_DOCKER_EXISTS: '1' });
  assert.equal(r.status, 0, r.stderr);
  const changes = r.calls.filter((c) => !/^(container|volume) inspect /.test(c));
  assert.deepEqual(changes, ['rm -f wf-s3-dev', 'volume rm wf-s3-dev']);

  const none = run(['down']);
  assert.equal(none.status, 0, none.stderr);
  assert.ok(
    none.calls.every((c) => /^(container|volume) inspect /.test(c)),
    `нечего удалять: ${none.calls}`,
  );
});
