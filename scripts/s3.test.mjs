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
      // init.sh: ключ служебного доступа — в формате вывода garage key info
      'case "$*" in *"key info --show-secret wf-dev-setup"*) printf "Key ID: GKsetup\\nSecret key: setup-secret\\n" ;; esac',
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

// scripts/s3/init.sh (ревью Task 3, m4, m5): служебный ключ wf-dev-setup с owner на wf-in удаляется при любом
// исходе; lifecycle wf-in сверяется по ответу хранилища. curl — фальшивый: PUT — успех (или FAKE_CURL_FAIL=1 —
// отказ), GET ?lifecycle — тело из FAKE_LIFECYCLE.
const INIT = path.join(import.meta.dirname, 's3', 'init.sh');
const LIFECYCLE =
  '<LifecycleConfiguration><Rule><ID>wf-in-1d</ID><Status>Enabled</Status><Filter><Prefix></Prefix></Filter>' +
  '<Expiration><Days>1</Days></Expiration></Rule></LifecycleConfiguration>';
const DELETE_SETUP = 'exec -e RUST_LOG=warn wf-s3-dev /garage key delete --yes wf-dev-setup';

function runInit(env) {
  writeFileSync(
    path.join(dir, 'curl'),
    [
      '#!/usr/bin/env bash',
      'cat >/dev/null',
      '[ "${FAKE_CURL_FAIL:-}" = 1 ] && { echo "curl: (7) Failed to connect" >&2; exit 7; }',
      'case " $* " in *" -X PUT "*) exit 0 ;; esac',
      'printf "%s" "$FAKE_LIFECYCLE"',
      '',
    ].join('\n'),
    { mode: 0o755 },
  );
  rmSync(log, { force: true });
  const r = spawnSync('bash', [INIT], {
    encoding: 'utf8',
    env: {
      ...process.env,
      PATH: dir + path.delimiter + process.env.PATH,
      FAKE_DOCKER_LOG: log,
      WF_S3_CONTAINER: 'wf-s3-dev',
      WF_S3_ENDPOINT: 'http://127.0.0.1:1',
      FAKE_LIFECYCLE: LIFECYCLE,
      ...env,
    },
  });
  const calls = existsSync(log) ? readFileSync(log, 'utf8').trim().split('\n') : [];
  return { status: r.status, stderr: r.stderr, calls };
}

test('init.sh: успех — служебный ключ удалён ровно один раз, секрет не в argv docker', () => {
  const r = runInit({});
  assert.equal(r.status, 0, r.stderr);
  assert.equal(r.calls.filter((c) => c === DELETE_SETUP).length, 1, r.calls.join('\n'));
  assert.ok(!r.calls.some((c) => c.includes('setup-secret')), 'секрет в аргументах docker');
});

test('init.sh: сбой посреди настройки — служебный ключ всё равно удалён', () => {
  const r = runInit({ FAKE_CURL_FAIL: '1' });
  assert.notEqual(r.status, 0);
  assert.match(r.stderr, /PUT wf-in\?lifecycle не прошёл/);
  assert.ok(r.calls.includes(DELETE_SETUP), `нет удаления wf-dev-setup:\n${r.calls.join('\n')}`);
});

test('init.sh: lifecycle wf-in не применён — отказ с ответом хранилища, ключ удалён', () => {
  for (const body of [
    '<LifecycleConfiguration/>',
    LIFECYCLE.replace('<Days>1</Days>', '<Days>7</Days>'),
  ]) {
    const r = runInit({ FAKE_LIFECYCLE: body });
    assert.notEqual(r.status, 0, body);
    assert.match(r.stderr, /lifecycle wf-in не применён/, body);
    assert.ok(r.calls.includes(DELETE_SETUP), `нет удаления wf-dev-setup:\n${r.calls.join('\n')}`);
  }
});
