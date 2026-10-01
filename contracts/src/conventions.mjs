// Конвенции контрактов (спека бэкенда §6.4, §6.8, §8.1–8.3): проверяются на собранных бандлах.

const VERBS = ['get', 'put', 'post', 'delete', 'options', 'head', 'patch', 'trace'];
const MUTATING = new Set(['post', 'put', 'patch', 'delete']);
const OPERATION_ID = /^[a-z][A-Za-z0-9]*$/;
const ERROR_CODE = /^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$/;
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

export function conventionViolations(doc) {
  const out = [];
  if (!Array.isArray(doc['x-error-codes-common'])) {
    out.push('нет x-error-codes-common — общих кодов ошибок платформы');
  }
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
        if (!ERROR_CODE.test(c)) out.push(`${where}: код ошибки «${c}» — нужен <модуль>.<ошибка>`);
      }
    }
    const security = op.security ?? doc.security ?? [];
    const hasKey = (op.parameters ?? []).some((p) => p.$ref === IDEMPOTENCY_REF);
    if (MUTATING.has(verb) && security.length > 0 && !hasKey) {
      out.push(`${where}: мутирующая операция с аутентификацией без Idempotency-Key`);
    }
  }
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
