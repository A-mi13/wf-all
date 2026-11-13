// Срез замороженного контракта identity (план I-1, R17): что хэширует contracts/test/identity-freeze.test.mjs.
//
// Граница (ревью плана I3, I5, o1): содержимое берётся только у «своего» — операций среза и компонентов, которые
// ввёл I-1 (все схемы identity/schemas.yaml и явный список own). Всё прочее, на что срез ссылается (схемы других
// модулей и платформы, общие компоненты корня), входит только строкой $ref: обход в них не спускается, поэтому
// правка чужого компонента срез не меняет.
import { createHash } from 'node:crypto';
import { operations } from './conventions.mjs';

// canonical — JSON с ключами по алфавиту; порядок массивов сохраняется.
export function canonical(node) {
  if (Array.isArray(node)) return `[${node.map(canonical).join(',')}]`;
  if (node && typeof node === 'object') {
    const keys = Object.keys(node).sort();
    return `{${keys.map((k) => `${JSON.stringify(k)}:${canonical(node[k])}`).join(',')}}`;
  }
  return JSON.stringify(node ?? null);
}

export const sha256 = (node) => createHash('sha256').update(canonical(node)).digest('hex');

// pick — значение по пути «kind/Name/…» внутри components; нет — undefined.
export function pick(doc, path) {
  return path.split('/').reduce((n, k) => (n == null ? undefined : n[k]), doc.components);
}

const VERBS = new Set(['get', 'put', 'post', 'delete', 'options', 'head', 'patch', 'trace']);

// byCodeUnits — порядок по кодовым единицам UTF-16, как Array.prototype.sort без компаратора: хэш не зависит от
// ICU и локали процесса (localeCompare зависел бы).
const byCodeUnits = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

// operationsOf — операции среза по operationId (порядок — по кодовым единицам), с методом, путём и ключами объекта
// пути кроме методов (parameters, summary, description, servers): параметр уровня пути действует на операцию.
export function operationsOf(doc, keep) {
  return operations(doc)
    .filter(({ op }) => keep(op))
    .sort((a, b) => byCodeUnits(a.op.operationId, b.op.operationId))
    .map(({ path, verb, op }) => ({
      path,
      verb,
      op,
      item: Object.fromEntries(Object.entries(doc.paths[path]).filter(([k]) => !VERBS.has(k))),
    }));
}

// requiredOwn — для частично своих схем (в own есть «schemas/X/properties/f», но не «schemas/X»): какие из своих
// полей стоят в required родителя. Обязательность своего поля — часть контракта (обязательное поле ответа
// генератор Dart делает ненулевым), чужие поля required в срез не входят.
export function requiredOwn(doc, own) {
  const fields = new Map();
  for (const p of own) {
    const m = /^schemas\/([^/]+)\/properties\/([^/]+)$/.exec(p);
    if (m && !own.includes(`schemas/${m[1]}`))
      fields.set(m[1], [...(fields.get(m[1]) ?? []), m[2]]);
  }
  const out = {};
  for (const [name, list] of [...fields].sort(([a], [b]) => byCodeUnits(a, b))) {
    const required = doc.components.schemas[name]?.required ?? [];
    out[`schemas/${name}`] = list.filter((f) => required.includes(f)).sort(byCodeUnits);
  }
  return out;
}

// slice — срез: операции целиком (с любыми x-…; $ref на компоненты — строкой) и свои компоненты по списку путей.
// Свои компоненты — по полному пути: «schemas/X» — схема целиком, «schemas/Problem/properties/reasons» — одно поле.
export function slice(doc, { keep, own, extra = {} }) {
  const ops = operationsOf(doc, keep);
  const components = {};
  for (const path of [...own].sort(byCodeUnits)) components[path] = pick(doc, path) ?? null;
  return { operations: ops, components, required: requiredOwn(doc, own), ...extra };
}

// section — раздел текста от маркера до следующего абзаца-заголовка «**…**» (или до конца).
export function section(text, marker) {
  const at = text.indexOf(marker);
  if (at < 0) return '';
  const rest = text.slice(at + marker.length);
  const next = rest.search(/\n\n\*\*[^*\n]+\*\*/);
  return marker + (next < 0 ? rest : rest.slice(0, next));
}

// bullet — пункт списка, начинающийся с маркера, до следующего пункта или пустой строки.
export function bullet(text, marker) {
  const at = text.indexOf(marker);
  if (at < 0) return '';
  const rest = text.slice(at);
  const end = rest.search(/\n(- |\n)/);
  return end < 0 ? rest : rest.slice(0, end);
}
