// Конвенции контрактов (спека бэкенда §6.4, §6.8, §8.1–8.3): проверяются на собранных бандлах.

const VERBS = ['get', 'put', 'post', 'delete', 'options', 'head', 'patch', 'trace'];
const MUTATING = new Set(['post', 'put', 'patch', 'delete']);
const OPERATION_ID = /^[a-z][A-Za-z0-9]*$/;
// <модуль>.<ошибка>[.<уточнение>…] — минимум два сегмента
const ERROR_CODE = /^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$/;
// единственный общий код без модуля
const COMMON_SINGLE = new Set(['internal']);
const IDEMPOTENCY_REF = '#/components/parameters/IdempotencyKey';

export function operations(doc) {
  return Object.entries(doc.paths ?? {}).flatMap(([path, item]) =>
    Object.entries(item)
      .filter(([verb]) => VERBS.includes(verb))
      .map(([verb, op]) => ({ path, verb, op })),
  );
}

function refs(node, out = []) {
  if (Array.isArray(node)) node.forEach((n) => refs(n, out));
  else if (node && typeof node === 'object') {
    for (const [k, v] of Object.entries(node)) {
      if (k === '$ref' && typeof v === 'string') out.push(v);
      else refs(v, out);
    }
  }
  return out;
}

// Ключи с данными, а не схемой: пример и расширения не проверяются. Под properties и schemas
// это имена (свойство «example» — схема), там пропуска нет.
const DATA_KEY = (k) => k === 'example' || k === 'examples' || k.startsWith('x-');
const NAME_MAPS = new Set(['properties', 'schemas']);

function closedEnums(node, path = [], out = []) {
  if (Array.isArray(node)) node.forEach((n, i) => closedEnums(n, [...path, i], out));
  else if (node && typeof node === 'object') {
    if (Array.isArray(node.enum) && node['x-extensible-enum'] !== true) out.push(path.join('.'));
    const names = NAME_MAPS.has(path.at(-1));
    for (const [k, v] of Object.entries(node)) {
      if (k !== 'enum' && (names || !DATA_KEY(k))) closedEnums(v, [...path, k], out);
    }
  }
  return out;
}

const nonEmpty = (a) => Array.isArray(a) && a.length > 0;

export function conventionViolations(doc) {
  const out = [];
  const rootSecurity = nonEmpty(doc.security);
  if (!rootSecurity) {
    out.push('нет аутентификации по умолчанию — корневая security пуста или не задана');
  }

  const common = doc['x-error-codes-common'];
  if (!Array.isArray(common)) {
    out.push('нет x-error-codes-common — общих кодов ошибок платформы');
  } else {
    for (const c of common) {
      if (typeof c !== 'string' || !(COMMON_SINGLE.has(c) || ERROR_CODE.test(c))) {
        out.push(`общий код ошибки «${c}» — нужен <область>.<ошибка>`);
      }
    }
  }

  // Анонимные мутирующие операции (регистрация, вход, коды) — только из закрытого списка в корне.
  const anonymous = doc['x-anonymous-mutations'];
  if (!Array.isArray(anonymous)) {
    out.push('нет x-anonymous-mutations — списка анонимных мутирующих операций');
  }
  const allowedAnonymous = new Set(Array.isArray(anonymous) ? anonymous : []);
  const actualAnonymous = new Set();
  let usesKey = false;

  for (const { path, verb, op } of operations(doc)) {
    const where = `${verb.toUpperCase()} ${path}`;
    if (!OPERATION_ID.test(op.operationId ?? '')) {
      out.push(`${where}: operationId «${op.operationId}» — нужен lowerCamel`);
    }
    if (op.tags?.length !== 1) out.push(`${where}: нужен ровно один тег — имя модуля`);
    const codes = op['x-error-codes'];
    if (!Array.isArray(codes)) out.push(`${where}: нет x-error-codes`);
    else {
      for (const c of codes) {
        if (typeof c !== 'string' || !ERROR_CODE.test(c)) {
          out.push(`${where}: код ошибки «${c}» — нужен <модуль>.<ошибка>`);
        }
      }
    }
    const hasKey = (op.parameters ?? []).some((p) => p.$ref === IDEMPOTENCY_REF);
    usesKey ||= hasKey;
    if (!MUTATING.has(verb)) continue;
    const security = op.security ?? doc.security ?? [];
    if (security.length > 0) {
      if (!hasKey) out.push(`${where}: мутирующая операция с аутентификацией без Idempotency-Key`);
    } else {
      actualAnonymous.add(op.operationId);
      if (!allowedAnonymous.has(op.operationId)) {
        out.push(`${where}: анонимная мутирующая операция не из x-anonymous-mutations`);
      }
    }
  }
  for (const id of allowedAnonymous) {
    if (!actualAnonymous.has(id)) {
      out.push(
        `x-anonymous-mutations: «${id}» — не анонимная мутирующая операция, убери из списка`,
      );
    }
  }

  // Валидатор запросов опирается на компонент: заголовок Idempotency-Key, обязательный.
  if (rootSecurity || usesKey) {
    const key = doc.components?.parameters?.IdempotencyKey;
    if (key?.name !== 'Idempotency-Key' || key?.in !== 'header' || key?.required !== true) {
      out.push(
        'components.parameters.IdempotencyKey — нужен заголовок Idempotency-Key с required: true',
      );
    }
  }

  // Спека §8.3: enum'ы открытые — клиент обязан пережить незнакомое значение, добавление значения
  // не ломает контракт. Генераторам (в том числе Dart) это сообщает x-extensible-enum.
  for (const where of closedEnums(doc)) out.push(`${where}: enum без x-extensible-enum: true`);

  for (const ref of refs(doc)) {
    if (!ref.startsWith('#/')) out.push(`внешний $ref ${ref} — контракт не собран`);
  }
  for (const name of Object.keys(doc.components?.schemas ?? {})) {
    if (/-\d+$/.test(name)) {
      out.push(`схема ${name}: redocly переименовал дубль — зарегистрируй схему в root.yaml`);
    }
  }
  return out;
}

export function errorCodes(doc) {
  const codes = [
    ...(doc['x-error-codes-common'] ?? []),
    ...operations(doc).flatMap(({ op }) => op['x-error-codes'] ?? []),
  ];
  return [...new Set(codes)].sort();
}
