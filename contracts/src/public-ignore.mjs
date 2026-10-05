// Страж файла исключений oasdiff публичного контракта (спека geo §4.4, спека бэкенда §8.3).
// До первого тега contracts-v1.0.0 осознанная ломающая правка public.yaml допустима строкой в
// contracts/oasdiff-err-ignore-public.txt; с тегом contracts-v1.0.0 или новее файл обязан быть
// пуст — ломать public можно только в /v2. Запуск — contracts/scripts/public-ignore.mjs из
// scripts/contracts-breaking.sh, первым шагом: без базы сравнения страж не пропускается.

// contracts-vX.Y.Z с X ≥ 1; пре-релиз (contracts-v1.0.0-rc.1) старше 1.0.0 и не считается.
const STABLE_TAG = /^contracts-v[1-9]\d*\.\d+\.\d+$/;

// stableTags — теги контракта версии 1.0.0 и новее, по алфавиту.
export function stableTags(tags) {
  return tags
    .map((t) => t.trim())
    .filter((t) => STABLE_TAG.test(t))
    .sort();
}

// ignoreLines — значимые строки файла исключений: пустые и из пробелов не считаются.
export function ignoreLines(text) {
  return text
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter((l) => l !== '');
}

// publicIgnoreViolation — текст нарушения или null: файл не пуст, а стабильный тег уже есть.
export function publicIgnoreViolation(tags, text) {
  const stable = stableTags(tags);
  const lines = ignoreLines(text);
  if (stable.length === 0 || lines.length === 0) return null;
  return (
    `в contracts/oasdiff-err-ignore-public.txt строк: ${lines.length}, а есть тег ${stable[0]} — ` +
    'после contracts-v1.0.0 ломать public можно только в /v2: очистите файл'
  );
}
