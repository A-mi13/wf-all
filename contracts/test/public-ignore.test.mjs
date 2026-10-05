// Страж файла исключений oasdiff публичного контракта (спека geo §4.4): до тега contracts-v1.0.0
// строки допустимы, с тегом contracts-v1.0.0 или новее файл обязан быть пуст.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { ignoreLines, publicIgnoreViolation, stableTags } from '../src/public-ignore.mjs';

const line = 'GET /v1/health api path removed without deprecation';

test('до тега contracts-v1.0.0 исключения public разрешены', () => {
  for (const tags of [
    [],
    ['contracts-v0.1.0', 'contracts-v0.9.3'],
    ['contracts-v1.0.0-rc.1'],
    ['v1.0.0', 'release-1.0.0', 'contracts-v01.0.0'],
  ]) {
    assert.equal(publicIgnoreViolation(tags, `${line}\n`), null, JSON.stringify(tags));
  }
});

test('с тегом contracts-v1.0.0 или новее файл обязан быть пуст', () => {
  for (const tags of [
    ['contracts-v1.0.0'],
    ['contracts-v0.9.0', 'contracts-v2.3.1'],
    ['contracts-v10.0.0'],
    [' contracts-v1.0.0\r'],
  ]) {
    const v = publicIgnoreViolation(tags, line);
    assert.match(v ?? '', /oasdiff-err-ignore-public\.txt/, JSON.stringify(tags));
    assert.match(v, /contracts-v\d+\.\d+\.\d+/);
  }
});

test('после тега пустой файл и строки из пробелов — не нарушение', () => {
  for (const text of ['', '\n', '  \r\n\t\n']) {
    assert.equal(publicIgnoreViolation(['contracts-v1.0.0'], text), null, JSON.stringify(text));
  }
});

test('stableTags — только X.Y.Z с X ≥ 1, по алфавиту; ignoreLines — без пустых строк', () => {
  assert.deepEqual(stableTags(['contracts-v2.0.0', 'contracts-v0.5.0', 'contracts-v1.1.0', '']), [
    'contracts-v1.1.0',
    'contracts-v2.0.0',
  ]);
  assert.deepEqual(ignoreLines(`${line}\r\n\r\n  ${line}  \n`), [line, line]);
});

// Страж подключения: скрипт CI зовёт страж до проверки базы (без базы он бы пропускался) и отдаёт
// oasdiff файл исключений каждого контракта.
test('scripts/contracts-breaking.sh вызывает страж и передаёт файлы исключений', async () => {
  const sh = await readFile(
    new URL('../../scripts/contracts-breaking.sh', import.meta.url),
    'utf8',
  );
  const guard = sh.search(/^node contracts\/scripts\/public-ignore\.mjs$/m);
  assert.ok(guard > 0, 'страж не вызывается');
  assert.ok(guard < sh.search(/^if ! git rev-parse --verify/m), 'страж стоит после проверки базы');
  assert.match(sh, /^for name in public admin; do$/m);
  assert.match(sh, /--err-ignore "contracts\/oasdiff-err-ignore-\$name\.txt"/);
});

// Запуск как в CI: настоящие теги и настоящий файл — модуль грузится, состояние репо допустимо.
test('страж проходит на состоянии репозитория', () => {
  const script = fileURLToPath(new URL('../scripts/public-ignore.mjs', import.meta.url));
  const r = spawnSync(process.execPath, [script], { encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
});
