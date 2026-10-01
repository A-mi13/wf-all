// Полнота документации контракта: Swagger (/docs) и сгенерированные клиенты читают описания и
// примеры прямо из бандла. Правило — у всего, что видит потребитель API, есть описание, у
// каждого значения — пример: нативщику и вебу не нужно спрашивать, что слать и что придёт.

import { operations } from './conventions.mjs';

const filled = (s) => typeof s === 'string' && s.trim() !== '';
const COMPONENT = /^#\/components\/(parameters|responses|requestBodies)\/(.+)$/;

// $ref на компонент того же вида; компонент проверяется по месту использования.
function resolve(doc, node) {
  const m = typeof node?.$ref === 'string' && COMPONENT.exec(node.$ref);
  return m ? (doc.components?.[m[1]]?.[m[2]] ?? {}) : node;
}

const hasExample = (n) =>
  n?.example !== undefined || (n?.examples && Object.keys(n.examples).length > 0);

// Поле со ссылкой на схему компонента: в OpenAPI 3.0 соседи $ref игнорируются, описание и
// примеры несёт сама схема — она проверяется в components.schemas.
function walkSchema(schema, where, out) {
  if (!schema || typeof schema !== 'object' || schema.$ref) return;
  for (const key of ['allOf', 'oneOf', 'anyOf']) {
    for (const part of schema[key] ?? []) walkSchema(part, where, out);
  }
  if (schema.properties) {
    for (const [name, prop] of Object.entries(schema.properties)) {
      if (prop?.$ref) continue;
      const at = `${where}.${name}`;
      if (!filled(prop?.description)) out.push(`${at}: нет description`);
      walkValue(prop, at, out);
    }
    return;
  }
  if (schema.items && !schema.$ref) walkValue(schema, where, out);
}

// Значение (поле, элемент массива, простая схема): составное — вглубь, простое — нужен пример.
// enum сам перечисляет допустимые значения — Swagger подставляет первое.
function walkValue(node, at, out) {
  if (node.properties || node.allOf || node.oneOf || node.anyOf) {
    walkSchema(node, at, out);
    return;
  }
  if (node.type === 'array' || node.items) {
    const items = node.items ?? {};
    if (items.$ref || items.properties || items.allOf || items.oneOf || items.anyOf) {
      walkSchema(items, `${at}[]`, out);
    } else if (!hasExample(node) && !hasExample(items) && !items.enum) {
      out.push(`${at}: нет примера`);
    }
    return;
  }
  if (!hasExample(node) && !node.enum) out.push(`${at}: нет примера`);
}

function walkContent(content, where, out) {
  for (const media of Object.values(content ?? {})) {
    if (hasExample(media)) continue;
    walkSchema(media.schema, where, out);
    if (media.schema && !media.schema.$ref && !media.schema.properties && !media.schema.items) {
      walkValue(media.schema, where, out);
    }
  }
}

export function docViolations(doc) {
  const out = [];
  if (!filled(doc.info?.description)) out.push('info.description: нет описания API');

  const tags = new Map((doc.tags ?? []).map((t) => [t.name, t]));
  const usedTags = new Set();

  for (const { path, verb, op } of operations(doc)) {
    const where = `${verb.toUpperCase()} ${path}`;
    for (const t of op.tags ?? []) usedTags.add(t);
    if (!filled(op.summary)) out.push(`${where}: нет summary`);
    if (!filled(op.description)) out.push(`${where}: нет description`);

    for (const raw of op.parameters ?? []) {
      const p = resolve(doc, raw);
      const at = `${where}: параметр «${p.name}»`;
      if (!filled(p.description)) out.push(`${at}: нет description`);
      if (!hasExample(p) && !hasExample(p.schema) && !p.schema?.enum) {
        out.push(`${at}: нет примера`);
      }
    }

    if (op.requestBody) {
      const body = resolve(doc, op.requestBody);
      const at = `${where}: тело запроса`;
      if (!filled(body.description)) out.push(`${at} — нет description`);
      walkContent(body.content, at, out);
    }

    for (const [status, raw] of Object.entries(op.responses ?? {})) {
      const r = resolve(doc, raw);
      const at = `${where}: ответ ${status}`;
      if (!filled(r.description)) out.push(`${at}: нет description`);
      walkContent(r.content, at, out);
    }
  }

  for (const t of usedTags) {
    if (!filled(tags.get(t)?.description)) {
      out.push(`тег «${t}»: нужен в корневом tags с description — это раздел Swagger`);
    }
  }

  for (const [name, schema] of Object.entries(doc.components?.schemas ?? {})) {
    const at = `схема ${name}`;
    if (schema?.$ref) continue;
    if (!filled(schema?.description)) out.push(`${at}: нет description`);
    if (schema?.properties || schema?.allOf || schema?.oneOf || schema?.anyOf) {
      walkSchema(schema, at, out);
    } else if (schema) {
      walkValue(schema, at, out);
    }
  }
  return out;
}
