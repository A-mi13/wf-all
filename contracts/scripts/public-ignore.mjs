// Запуск стража файла исключений public (src/public-ignore.mjs) из scripts/contracts-breaking.sh:
// теги — git tag, файл — contracts/oasdiff-err-ignore-public.txt. Нарушение — текст в stderr и
// код выхода 1; нет файла — ошибка чтения (oasdiff без него тоже не запустится).
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { publicIgnoreViolation } from '../src/public-ignore.mjs';

const repo = fileURLToPath(new URL('../..', import.meta.url));
const tags = execFileSync('git', ['tag', '--list', 'contracts-v*'], {
  cwd: repo,
  encoding: 'utf8',
}).split('\n');
const text = readFileSync(new URL('../oasdiff-err-ignore-public.txt', import.meta.url), 'utf8');
const violation = publicIgnoreViolation(tags, text);
if (violation) {
  console.error(`contracts:breaking: ОШИБКА — ${violation}`);
  process.exit(1);
}
