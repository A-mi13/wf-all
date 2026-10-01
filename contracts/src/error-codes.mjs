// Коды ошибок собранного контракта — для проверки локалей приложений (спека §12.5).
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { parse } from 'yaml';
import { errorCodes } from './conventions.mjs';

// Путь строится через import.meta.dirname, а не new URL(): в vitest с jsdom глобальный URL — не
// узловой, и readFile принимает его за строку (ENOENT на «\@fs\F:\…»).
export async function loadErrorCodes(name) {
  const doc = parse(
    await readFile(join(import.meta.dirname, '..', 'openapi', `${name}.yaml`), 'utf8'),
  );
  return errorCodes(doc);
}
