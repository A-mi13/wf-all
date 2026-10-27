// Каталоги подписей админки (спека сотрудников §9.5, решение D21): каждый нечерновой ключ прав
// (permissions.<ключ>) и каждое действие подтверждения (approvals.actions.<действие>) обязаны
// иметь текст в apps/admin/messages/{ru,en}.json. Источники — каталог прав
// openapi/admin/permissions.json и enum схемы ApprovalAction собранного контракта admin.
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { parse } from 'yaml';

// catalog — содержимое permissions.json: { service: [...], permissions: [{ key, draft, mutating }] }.
export function permissionKeys(catalog) {
  return catalog.permissions.filter((p) => !p.draft).map((p) => p.key);
}

export function approvalActions(doc) {
  const values = doc.components?.schemas?.ApprovalAction?.enum;
  if (!Array.isArray(values)) throw new Error('в контракте нет enum схемы ApprovalAction');
  return [...values].sort();
}

// Путь — через import.meta.dirname (см. error-codes.mjs: в vitest с jsdom URL не узловой).
export async function loadLabelCatalog() {
  const dir = join(import.meta.dirname, '..', 'openapi');
  const [catalog, doc] = await Promise.all([
    readFile(join(dir, 'admin', 'permissions.json'), 'utf8').then(JSON.parse),
    readFile(join(dir, 'admin.yaml'), 'utf8').then(parse),
  ]);
  return { permissions: permissionKeys(catalog), approvalActions: approvalActions(doc) };
}
