// Страж прав контракта admin (спека сотрудников §2.4, §8; решения D14, D15). Конвейер admin-api
// проверяет право каждой операции по x-permission из бандла, поэтому ошибка в расширении — дыра
// в доступе, а не опечатка в документации. Каталог прав — openapi/admin/permissions.json
// (генерирует ./task gen из backend/internal/platform/perm).

import { operations } from './conventions.mjs';

// Поля ответа с секретами: их ответ не сохраняется в idempotency_keys (D14).
const SECRET_FIELDS = ['secret', 'otpauth_uri', 'recovery_codes'];
const NO_STORE = 'no-store';
const SCHEMA_REF = /^#\/components\/(schemas|responses)\/(.+)$/;

// Ссылка на компонент бандла (#/components/schemas/X, #/components/responses/X); иное — как есть.
function deref(doc, node) {
  const m = typeof node?.$ref === 'string' && SCHEMA_REF.exec(node.$ref);
  return m ? doc.components?.[m[1]]?.[m[2]] : node;
}

// Имена свойств схемы на любой глубине: properties, items, allOf/oneOf/anyOf,
// additionalProperties; ссылки разворачиваются, повторная — пропускается (циклы схем).
function propertyNames(doc, node, seen, out) {
  if (!node || typeof node !== 'object') return out;
  if (typeof node.$ref === 'string') {
    if (seen.has(node.$ref)) return out;
    seen.add(node.$ref);
    return propertyNames(doc, deref(doc, node), seen, out);
  }
  for (const [name, prop] of Object.entries(node.properties ?? {})) {
    out.add(name);
    propertyNames(doc, prop, seen, out);
  }
  for (const key of ['allOf', 'oneOf', 'anyOf']) {
    for (const part of node[key] ?? []) propertyNames(doc, part, seen, out);
  }
  propertyNames(doc, node.items, seen, out);
  if (typeof node.additionalProperties === 'object') {
    propertyNames(doc, node.additionalProperties, seen, out);
  }
  return out;
}

// Поля-секреты во всех ответах операции (схемы тел всех статусов и типов содержимого).
function secretFields(doc, op) {
  const names = new Set();
  for (const raw of Object.values(op.responses ?? {})) {
    const seen = new Set();
    const r = typeof raw?.$ref === 'string' ? (seen.add(raw.$ref), deref(doc, raw)) : raw;
    for (const media of Object.values(r?.content ?? {})) {
      propertyNames(doc, media?.schema, seen, names);
    }
  }
  return SECRET_FIELDS.filter((f) => names.has(f));
}

// catalog — содержимое permissions.json: { service: [...], permissions: [{ key, draft, mutating }] }.
export function permissionViolations(doc, catalog) {
  const service = new Set(catalog.service);
  const byKey = new Map(catalog.permissions.map((p) => [p.key, p]));
  const out = [];

  // ключ права из каталога, не черновой; для GET — не изменяющий (чтение не может требовать
  // права на изменение: в матрице такая операция досталась бы не тем ролям)
  const checkKey = (where, verb, key, label) => {
    const p = byKey.get(key);
    if (!p) {
      out.push(`${where}: ${label} «${key}» — нет в каталоге прав (permissions.json)`);
    } else if (p.draft) {
      out.push(`${where}: ${label} «${key}» — черновое право: операции на нём не строятся`);
    } else if (verb === 'get' && p.mutating) {
      out.push(`${where}: GET с изменяющим правом ${key} — чтению нужно право на чтение`);
    }
  };

  for (const { path, verb, op } of operations(doc)) {
    const where = `${verb.toUpperCase()} ${path}`;
    const perm = op['x-permission'];
    const any = op['x-permission-any'];

    if (typeof perm !== 'string' || perm === '') {
      out.push(`${where}: нет x-permission — конвейер не знает, кого пускать`);
    } else if (!service.has(perm)) {
      checkKey(where, verb, perm, 'x-permission');
    }

    // handler ⇔ непустой x-permission-any: право зависит от тела или цели, конвейер требует хотя
    // бы одно из перечисленных, точную проверку делает сценарий
    if (perm === 'handler') {
      if (!Array.isArray(any) || any.length === 0) {
        out.push(`${where}: x-permission: handler без x-permission-any — непустого списка прав`);
      } else {
        for (const key of any) checkKey(where, verb, key, 'x-permission-any:');
      }
    } else if (any !== undefined) {
      out.push(`${where}: x-permission-any — только при x-permission: handler`);
    }

    // anonymous ⇔ security: [] — без сессии пускают только явно анонимные операции
    const anonymousSecurity = Array.isArray(op.security) && op.security.length === 0;
    if (perm === 'anonymous' && !anonymousSecurity) {
      out.push(`${where}: x-permission: anonymous — только с security: []`);
    }
    if (anonymousSecurity && perm !== 'anonymous') {
      out.push(`${where}: security: [] — только с x-permission: anonymous`);
    }

    // x-idempotency: только no-store. Любое другое значение middleware не узнаёт и молча хранит
    // тело ответа — секрет остался бы в idempotency_keys (fail-open)
    const idem = op['x-idempotency'];
    if (idem !== undefined && idem !== NO_STORE) {
      out.push(`${where}: x-idempotency «${idem}» — допустимо только ${NO_STORE}`);
    }
    if (idem !== NO_STORE) {
      for (const field of secretFields(doc, op)) {
        out.push(`${where}: в ответе поле ${field} — нужен x-idempotency: ${NO_STORE}`);
      }
    }
  }
  return out;
}
