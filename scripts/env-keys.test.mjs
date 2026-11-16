// scripts/env-keys.sh: плейсхолдеры ключей заменяются случайными значениями; кольцо identity у api и воркера — одно
// значение (письма с кодами собирает воркер тем же кольцом, план I-2 Task 4).
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { test } from 'node:test';
import assert from 'node:assert/strict';

const SCRIPT = path.join(import.meta.dirname, 'env-keys.sh');
const PH = '<base64-32-байта>';

function run(content) {
  const dir = mkdtempSync(path.join(tmpdir(), 'wf-env-keys-'));
  try {
    const f = path.join(dir, '.env');
    writeFileSync(f, content);
    const r = spawnSync('bash', [SCRIPT, f], { encoding: 'utf8' });
    assert.equal(r.status, 0, r.stderr);
    const out = readFileSync(f, 'utf8');
    return {
      out,
      stdout: r.stdout,
      stderr: r.stderr,
      env: Object.fromEntries(
        out
          .split('\n')
          .filter((l) => /^[A-Z]/.test(l))
          .map((l) => [l.slice(0, l.indexOf('=')), l.slice(l.indexOf('=') + 1)]),
      ),
    };
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

test('плейсхолдеры заменены, ключи разные, значения не выводятся', () => {
  const { env, stdout } = run(
    `API_JWT_SEEDS=${PH}\nAPI_HUMANCHECK_KEYS=${PH}\nAPI_IDENTITY_DEVICE_KEYS=${PH}\nX=1\n`,
  );
  for (const k of ['API_JWT_SEEDS', 'API_HUMANCHECK_KEYS', 'API_IDENTITY_DEVICE_KEYS']) {
    assert.equal(Buffer.from(env[k], 'base64').length, 32, k);
    assert.ok(!stdout.includes(env[k]), `${k} в выводе`);
  }
  assert.notEqual(env.API_JWT_SEEDS, env.API_HUMANCHECK_KEYS);
  assert.notEqual(env.API_IDENTITY_DEVICE_KEYS, env.API_JWT_SEEDS);
  assert.equal(env.X, '1');
});

test('кольцо identity: api и воркер получают одно значение, отдельное от прочих ключей', () => {
  const { env, stdout } = run(
    `API_IDENTITY_SECRET_KEYS=${PH}\nAPI_IDENTITY_DEVICE_KEYS=${PH}\nWORKER_IDENTITY_SECRET_KEYS=${PH}\n`,
  );
  assert.equal(Buffer.from(env.API_IDENTITY_SECRET_KEYS, 'base64').length, 32);
  assert.equal(env.WORKER_IDENTITY_SECRET_KEYS, env.API_IDENTITY_SECRET_KEYS);
  assert.notEqual(env.API_IDENTITY_DEVICE_KEYS, env.API_IDENTITY_SECRET_KEYS);
  assert.ok(!stdout.includes(env.API_IDENTITY_SECRET_KEYS));
});

test('кольцо identity уже заполнено у одного — второй получает то же значение, заполненное не меняется', () => {
  const ring =
    'Zm9vYmFyYmF6cXV4Zm9vYmFyYmF6cXV4Zm9vYmFyYmE=,b2xkb2xkb2xkb2xkb2xkb2xkb2xkb2xkb2xkb2xkb2w=';
  for (const content of [
    `API_IDENTITY_SECRET_KEYS=${ring}\nWORKER_IDENTITY_SECRET_KEYS=${PH}\n`,
    `WORKER_IDENTITY_SECRET_KEYS=${PH}\nAPI_IDENTITY_SECRET_KEYS=${ring}\n`,
    `WORKER_IDENTITY_SECRET_KEYS=${ring}\nAPI_IDENTITY_SECRET_KEYS=${PH}\n`,
  ]) {
    const { env } = run(content);
    assert.equal(env.API_IDENTITY_SECRET_KEYS, ring, content);
    assert.equal(env.WORKER_IDENTITY_SECRET_KEYS, ring, content);
  }
});

test('кольцо identity заполнено у обоих разными значениями — предупреждение без значений, файл не меняется', () => {
  const ringA = 'Zm9vYmFyYmF6cXV4Zm9vYmFyYmF6cXV4Zm9vYmFyYmE=';
  const ringB = 'b2xkb2xkb2xkb2xkb2xkb2xkb2xkb2xkb2xkb2xkb2w=';
  const { env, stdout, stderr } = run(
    `API_IDENTITY_SECRET_KEYS=${ringA}\nWORKER_IDENTITY_SECRET_KEYS=${ringB}\n`,
  );
  assert.equal(env.API_IDENTITY_SECRET_KEYS, ringA);
  assert.equal(env.WORKER_IDENTITY_SECRET_KEYS, ringB);
  assert.match(stderr, /API_IDENTITY_SECRET_KEYS/);
  assert.match(stderr, /WORKER_IDENTITY_SECRET_KEYS/);
  for (const v of [ringA, ringB]) {
    assert.ok(!stderr.includes(v) && !stdout.includes(v), 'значение в выводе');
  }
});

test('кольцо identity у обоих одинаковое — без предупреждения', () => {
  const ring = 'Zm9vYmFyYmF6cXV4Zm9vYmFyYmF6cXV4Zm9vYmFyYmE=';
  const { stderr } = run(`API_IDENTITY_SECRET_KEYS=${ring}\nWORKER_IDENTITY_SECRET_KEYS=${ring}\n`);
  assert.equal(stderr, '');
});
