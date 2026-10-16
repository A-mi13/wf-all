import type { components } from '../api/gen/admin';
import { http, server } from './server';
import { approval } from './staff';

// Сервер «Подтверждений» для моков — по схемам контракта admin.d.ts (Approval, ApprovalPage): списки
// box=incoming|mine (status, курсор по страницам), решения approve/reject/cancel. Ответ решения — из
// answer (Response — ошибка или свой ответ), иначе запрос в итоговом состоянии. seen — что спрашивал
// экран.

type Approval = components['schemas']['Approval'];
type Decision = 'approve' | 'reject' | 'cancel';

export const OLGA = { id: '0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c6d', label: 'Ольга В.' };
export const IGOR = { id: '0192f1c4-0000-7b21-9c5d-0000000000ad', label: 'Игорь Л.' };

// Входящий запрос: пригласить модератора, IDN-почта как хранится (A-форма xn--), инициатор — Игорь.
export const INCOMING = approval({
  id: '0192f1e2-4c5d-7e6f-a081-000000000001',
  action: 'staff.invite',
  permission: 'staff.roles.grant',
  payload: {
    v: 1,
    email: 'ivan@xn--d1acufc.xn--p1ai',
    display_name: 'Иван П.',
    role: 'moderator',
    city_ids: ['3f2c8e1a-9b4d-4c7e-a6f1-2d8b5e0c9a47'],
  },
  requested_by: IGOR,
});

// Свой запрос, ожидающий: сброс доступа Марии.
export const MINE_PENDING = approval({
  id: '0192f1e2-4c5d-7e6f-a081-000000000002',
  action: 'staff.access_reset',
  permission: 'staff.access.reset',
  target_id: '0192f1c4-1111-7b21-9c5d-000000000001',
  payload: {
    v: 1,
    target_id: '0192f1c4-1111-7b21-9c5d-000000000001',
    version: 3,
    role: 'moderator',
    reason: 'security',
    comment: 'потерян телефон',
  },
  requested_by: OLGA,
});

// Свой запрос, не исполнился: смена роли, цель уже изменилась (решил Игорь).
export const MINE_FAILED = approval({
  id: '0192f1e2-4c5d-7e6f-a081-000000000003',
  action: 'staff.role_change',
  permission: 'staff.roles.grant',
  status: 'failed',
  target_id: '0192f1c4-1111-7b21-9c5d-000000000001',
  payload: {
    v: 1,
    target_id: '0192f1c4-1111-7b21-9c5d-000000000001',
    version: 3,
    from_role: 'moderator',
    from_city_ids: [],
    role: 'support',
    city_ids: [],
  },
  requested_by: OLGA,
  decided_by: IGOR,
  decided_at: '2026-10-13T10:00:00Z',
  comment: 'ок',
  error: 'staff.last_owner',
});

// Свой запрос, погашен системой: инициатор сменился (решившего нет).
export const MINE_SYSTEM = approval({
  id: '0192f1e2-4c5d-7e6f-a081-000000000004',
  action: 'staff.revoke_owner',
  permission: 'staff.roles.revoke_owner',
  status: 'cancelled',
  target_id: '0192f1c4-1111-7b21-9c5d-000000000009',
  payload: {
    v: 1,
    target_id: '0192f1c4-1111-7b21-9c5d-000000000009',
    version: 1,
    role: 'owner',
    reason: 'left_team',
  },
  requested_by: OLGA,
  decided_by: null,
  decided_at: '2026-10-13T11:00:00Z',
  error: 'initiator_changed',
});

export function approvalsServer(
  opts: {
    incoming?: Approval[];
    mine?: Approval[];
    pageSize?: number;
    answer?: Partial<Record<Decision, (a: Approval, body: unknown) => Response | Approval | null>>;
  } = {},
) {
  const state = { incoming: opts.incoming ?? [INCOMING], mine: opts.mine ?? [] };
  const seen = {
    lists: [] as URLSearchParams[],
    background: [] as (string | null)[],
    decisions: [] as { kind: Decision; id: string; body: unknown; key: string | null }[],
  };
  const size = opts.pageSize ?? 20;
  const all = () => [...state.incoming, ...state.mine];

  server.use(
    http.get('/v1/approvals', ({ request, response }) => {
      const q = new URL(request.url).searchParams;
      seen.lists.push(q);
      seen.background.push(request.headers.get('X-WF-Background'));
      const box = q.get('box') === 'mine' ? state.mine : state.incoming;
      const status = q.get('status');
      const rows = box.filter((a) => !status || a.status === status);
      const from = Number(q.get('cursor') ?? 0);
      const limit = Number(q.get('limit') ?? size);
      const items = rows.slice(from, from + Math.min(limit, size));
      const next = from + items.length < rows.length ? String(from + items.length) : null;
      return response(200).json({ items, next_cursor: next });
    }),
  );
  // Итог решения: ответ из answer (ошибка или свой запрос) или запрос в состоянии status.
  const decide = (
    kind: Decision,
    status: Approval['status'],
    id: string,
    body: unknown,
    key: string | null,
  ) => {
    seen.decisions.push({ kind, id, body, key });
    const target = all().find((a) => a.id === id) ?? approval({ id });
    const r = opts.answer?.[kind]?.(target, body);
    if (r instanceof Response) return r;
    const out: Approval = r ?? {
      ...target,
      status,
      decided_by: OLGA,
      decided_at: '2026-10-13T12:00:00Z',
    };
    // решённый уходит из входящих (как на сервере: входящие — pending)
    if (out.status !== 'pending') state.incoming = state.incoming.filter((a) => a.id !== out.id);
    state.mine = state.mine.map((a) => (a.id === out.id ? out : a));
    return out;
  };
  const json = async (request: Request) => request.json().catch(() => null) as Promise<unknown>;
  server.use(
    http.post('/v1/approvals/{approvalId}/approve', async ({ params, request, response }) => {
      const r = decide(
        'approve',
        'executed',
        params.approvalId,
        await json(request),
        request.headers.get('Idempotency-Key'),
      );
      return r instanceof Response ? response.untyped(r) : response(200).json(r);
    }),
    http.post('/v1/approvals/{approvalId}/reject', async ({ params, request, response }) => {
      const r = decide(
        'reject',
        'rejected',
        params.approvalId,
        await json(request),
        request.headers.get('Idempotency-Key'),
      );
      return r instanceof Response ? response.untyped(r) : response(200).json(r);
    }),
    http.post('/v1/approvals/{approvalId}/cancel', ({ params, request, response }) => {
      const r = decide(
        'cancel',
        'cancelled',
        params.approvalId,
        null,
        request.headers.get('Idempotency-Key'),
      );
      return r instanceof Response ? response.untyped(r) : response(200).json(r);
    }),
  );
  return { state, seen };
}
