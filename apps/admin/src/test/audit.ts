import type { components } from '../api/gen/admin';
import { http, server } from './server';

// «Журнал действий» для моков — по схеме AuditRecord контракта admin.d.ts: записи и сервер listAudit
// (фильтры — как на сервере: action_prefix — начало кода, остальное — точное совпадение; курсор по
// страницам). seen — какие параметры спрашивал экран.

type AuditRecord = components['schemas']['AuditRecord'];

export const MARIA_ID = '0192f1c4-1111-7b21-9c5d-000000000001';

// Запись по умолчанию: Ольга В. (владелец) сменила роль Марии — было/стало, причина, сеть.
export function auditRecord(over: Partial<AuditRecord> = {}): AuditRecord {
  return {
    id: '0192f1f0-8a9b-7c0d-9e1f-2a3b4c5d6e7f',
    created_at: '2026-10-13T09:20:00Z',
    action: 'staff.role_changed',
    object_type: 'staff_account',
    object_id: MARIA_ID,
    actor_user_id: null,
    actor_staff_id: '0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c6d',
    actor_role: 'owner',
    actor_label: 'Ольга В.',
    scope_city_id: null,
    reason: 'role_change: новые обязанности',
    ip: '203.0.113.0/24',
    user_agent: 'Mozilla/5.0 (Macintosh) Safari/605.1.15',
    before: { role: 'support', city_ids: [] },
    after: { role: 'moderator', city_ids: ['3f2c8e1a-9b4d-4c7e-a6f1-2d8b5e0c9a47'] },
    ...over,
  };
}

export const LOG: AuditRecord[] = [
  auditRecord(),
  auditRecord({
    id: '0192f1f0-8a9b-7c0d-9e1f-000000000002',
    created_at: '2026-10-13T09:15:00Z',
    action: 'platform.approval_executed',
    object_type: 'approval',
    object_id: '0192f1e2-4c5d-7e6f-a081-92b3c4d5e6f7',
    actor_staff_id: '0192f1c4-0000-7b21-9c5d-0000000000ad',
    actor_role: 'admin',
    actor_label: 'Игорь Л.',
    reason: null,
    before: null,
    after: { action: 'staff.role_change', status: 'executed' },
  }),
  auditRecord({
    id: '0192f1f0-8a9b-7c0d-9e1f-000000000003',
    created_at: '2026-10-13T03:00:00Z',
    action: 'platform.approval_expired',
    object_type: 'approval',
    object_id: '0192f1e2-4c5d-7e6f-a081-000000000003',
    actor_staff_id: null,
    actor_role: 'system',
    actor_label: 'system',
    reason: null,
    ip: null,
    user_agent: null,
    before: null,
    after: { status: 'expired' },
  }),
  auditRecord({
    id: '0192f1f0-8a9b-7c0d-9e1f-000000000004',
    created_at: '2026-10-12T18:00:00Z',
    action: 'staff.invite_resent',
    actor_staff_id: null,
    actor_role: 'operator',
    actor_label: 'operator',
    reason: null,
    ip: null,
    user_agent: null,
    before: null,
    after: null,
  }),
];

export function auditServer(opts: { records?: AuditRecord[]; pageSize?: number } = {}) {
  const records = opts.records ?? LOG;
  const seen: URLSearchParams[] = [];
  const size = opts.pageSize ?? 20;
  server.use(
    http.get('/v1/audit', ({ request, response }) => {
      const q = new URL(request.url).searchParams;
      seen.push(q);
      const prefix = q.get('action_prefix') ?? '';
      const rows = records.filter(
        (r) =>
          r.action.startsWith(prefix) &&
          (!q.get('actor_staff_id') || r.actor_staff_id === q.get('actor_staff_id')) &&
          (!q.get('object_type') || r.object_type === q.get('object_type')) &&
          (!q.get('object_id') || r.object_id === q.get('object_id')) &&
          (!q.get('from') || r.created_at >= (q.get('from') ?? '')) &&
          (!q.get('to') || r.created_at < (q.get('to') ?? '')),
      );
      const from = Number(q.get('cursor') ?? 0);
      const items = rows.slice(from, from + size);
      const next = from + size < rows.length ? String(from + size) : null;
      return response(200).json({ items, next_cursor: next });
    }),
  );
  return seen;
}
