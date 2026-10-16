// Контракт admin плана A (спека сотрудников §4.3–4.9, §5.3, §6, §8; план — R8, R13–R15, P8, P28;
// сверка reconcile §7–8): пути, права, анонимность, форма ответов, cookie и заголовки — на собранном
// бандле. После Task 9a контракт заморожен (P16): фронт и бэкенд работают по нему.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { parse } from 'yaml';
import { operations } from '../src/conventions.mjs';

const doc = parse(await readFile(new URL('../openapi/admin.yaml', import.meta.url), 'utf8'));
const ops = new Map(operations(doc).map((o) => [o.op.operationId, o]));
const resolve = (node, kind) =>
  node?.$ref ? doc.components[kind][node.$ref.split('/').at(-1)] : node;
const schema = (name) => doc.components.schemas[name];
const refName = (s) => s?.$ref?.split('/').at(-1);

// R8: operationId → [метод, путь, тег, x-permission, x-permission-any]
const GRANT = ['staff.roles.grant', 'staff.roles.grant_elevated'];
const DUAL = [
  'staff.roles.grant_elevated',
  'staff.roles.grant',
  'staff.roles.revoke_owner',
  'staff.access.reset_elevated',
  'staff.access.reset',
];
const expected = {
  staffLogin: ['post', '/v1/staff/auth/login', 'staff', 'anonymous'],
  staffAcceptInvite: ['post', '/v1/staff/auth/invite/accept', 'staff', 'anonymous'],
  staffResendInviteSelf: ['post', '/v1/staff/auth/invite/resend', 'staff', 'anonymous'],
  staffVerifyMfa: ['post', '/v1/staff/auth/mfa', 'staff', 'session'],
  staffUnlock: ['post', '/v1/staff/auth/unlock', 'staff', 'session'],
  staffLogout: ['post', '/v1/staff/auth/logout', 'staff', 'session'],
  getStaffMe: ['get', '/v1/staff/me', 'staff', 'session'],
  staffMfaSetup: ['post', '/v1/staff/me/mfa/setup', 'staff', 'session'],
  staffMfaConfirm: ['post', '/v1/staff/me/mfa/confirm', 'staff', 'session'],
  staffRecoveryAck: ['post', '/v1/staff/me/mfa/recovery-codes/ack', 'staff', 'session'],
  getStaffMfa: ['get', '/v1/staff/me/mfa', 'staff', 'self'],
  regenerateStaffRecoveryCodes: ['post', '/v1/staff/me/mfa/recovery-codes', 'staff', 'self'],
  changeStaffPassword: ['post', '/v1/staff/me/password', 'staff', 'self'],
  listStaffMySessions: ['get', '/v1/staff/me/sessions', 'staff', 'self'],
  revokeStaffMySession: ['delete', '/v1/staff/me/sessions/{sessionId}', 'staff', 'self'],
  revokeStaffOtherSessions: ['post', '/v1/staff/me/sessions/revoke-others', 'staff', 'self'],
  updateStaffMeLocale: ['patch', '/v1/staff/me/locale', 'staff', 'self'],
  createStaffInvite: ['post', '/v1/staff/invites', 'staff', 'handler', GRANT],
  changeStaffRole: [
    'put',
    '/v1/staff/accounts/{staffId}/role',
    'staff',
    'handler',
    [...GRANT, 'staff.roles.revoke'],
  ],
  disableStaffAccount: [
    'post',
    '/v1/staff/accounts/{staffId}/disable',
    'staff',
    'handler',
    ['staff.roles.revoke', 'staff.roles.revoke_admin', 'staff.roles.revoke_owner'],
  ],
  resetStaffAccess: [
    'post',
    '/v1/staff/accounts/{staffId}/access-reset',
    'staff',
    'handler',
    ['staff.access.reset', 'staff.access.reset_elevated'],
  ],
  listStaffAccounts: ['get', '/v1/staff/accounts', 'staff', 'staff.accounts.read'],
  getStaffAccount: ['get', '/v1/staff/accounts/{staffId}', 'staff', 'staff.accounts.read'],
  resendStaffInvite: [
    'post',
    '/v1/staff/accounts/{staffId}/invite/resend',
    'staff',
    'staff.invites.resend',
  ],
  listStaffAccountSessions: [
    'get',
    '/v1/staff/accounts/{staffId}/sessions',
    'staff',
    'staff.accounts.read',
  ],
  revokeStaffAccountSessions: [
    'post',
    '/v1/staff/accounts/{staffId}/sessions/revoke-all',
    'staff',
    'staff.sessions.revoke',
  ],
  getStaffRoles: ['get', '/v1/staff/roles', 'staff', 'staff.roles.read'],
  listAudit: ['get', '/v1/audit', 'platform', 'audit.log.read'],
  listApprovals: ['get', '/v1/approvals', 'platform', 'self'],
  cancelApproval: ['post', '/v1/approvals/{approvalId}/cancel', 'platform', 'self'],
  approveApproval: ['post', '/v1/approvals/{approvalId}/approve', 'platform', 'handler', DUAL],
  rejectApproval: ['post', '/v1/approvals/{approvalId}/reject', 'platform', 'handler', DUAL],
  listGeoCities: ['get', '/v1/geo/cities', 'geo', 'geo.cities.read'],
};

test('33 операции плана A — и только они', () => {
  assert.equal(Object.keys(expected).length, 33);
  assert.deepEqual([...ops.keys()].sort(), Object.keys(expected).sort());
  assert.equal(Object.keys(doc.paths).length, 33);
});

for (const [id, [verb, path, tag, perm, any]] of Object.entries(expected)) {
  test(`${id}: ${verb.toUpperCase()} ${path}, тег ${tag}, x-permission ${perm}`, () => {
    const o = ops.get(id);
    assert.ok(o, `нет операции ${id}`);
    assert.equal(o.verb, verb);
    assert.equal(o.path, path);
    assert.deepEqual(o.op.tags, [tag]);
    assert.equal(o.op['x-permission'], perm);
    assert.deepEqual(o.op['x-permission-any'], any);
    assert.equal(Array.isArray(o.op.security) && o.op.security.length === 0, perm === 'anonymous');
  });
}

test('/v1/health удалён из контракта admin (R13)', () => {
  assert.equal(doc.paths['/v1/health'], undefined);
  assert.equal(schema('Health'), undefined);
});

test('x-anonymous-mutations — ровно три анонимные операции', () => {
  assert.deepEqual([...doc['x-anonymous-mutations']].sort(), [
    'staffAcceptInvite',
    'staffLogin',
    'staffResendInviteSelf',
  ]);
});

test('cookie сессии — __Host-wf_admin_session (M11)', () => {
  assert.deepEqual(doc.security, [{ session: [] }]);
  const s = doc.components.securitySchemes.session;
  assert.deepEqual([s.type, s.in, s.name], ['apiKey', 'cookie', '__Host-wf_admin_session']);
});

const MUTATING = new Set(['post', 'put', 'patch', 'delete']);
const adminHeader = (op) =>
  (op.parameters ?? []).some((p) => resolve(p, 'parameters')?.name === 'X-WF-Admin');

test('X-WF-Admin: 1 — обязательный заголовок каждой мутации (M13), у чтения его нет', () => {
  const p = doc.components.parameters.AdminHeader;
  assert.deepEqual([p.name, p.in, p.required], ['X-WF-Admin', 'header', true]);
  assert.equal(p.schema.pattern, '^1$');
  for (const [id, { verb, op }] of ops) {
    assert.equal(adminHeader(op), MUTATING.has(verb), id);
  }
});

const successes = (op) => Object.keys(op.responses).filter((s) => s !== 'default');
const body = (op, status) =>
  resolve(op.responses[status], 'responses').content?.['application/json'];

test('204 без тела — выход, второй фактор, «Мой доступ», повторная отправка (reconcile §8)', () => {
  for (const id of [
    'staffVerifyMfa',
    'staffUnlock',
    'staffLogout',
    'staffRecoveryAck',
    'changeStaffPassword',
    'revokeStaffMySession',
    'resendStaffInvite',
    'updateStaffMeLocale',
  ]) {
    const { op } = ops.get(id);
    assert.deepEqual(successes(op), ['204'], id);
    assert.equal(op.responses['204'].content, undefined, id);
  }
});

test('200 {stage} — вход и принятие приглашения', () => {
  for (const id of ['staffLogin', 'staffAcceptInvite']) {
    const { op } = ops.get(id);
    assert.deepEqual(successes(op), ['200'], id);
    assert.equal(refName(body(op, '200').schema), 'StaffStageResult', id);
  }
  assert.equal(refName(schema('StaffStageResult').properties.stage), 'StaffStage');
  assert.deepEqual(schema('StaffStage').enum, ['mfa_pending', 'setup', 'active']);
});

test('202 {approval} — приглашение и сброс доступа; 200 StaffAccount | 202 — роль и выключение', () => {
  for (const id of ['createStaffInvite', 'resetStaffAccess']) {
    const { op } = ops.get(id);
    assert.deepEqual(successes(op), ['202'], id);
    assert.equal(refName(body(op, '202').schema), 'ApprovalAccepted', id);
  }
  for (const id of ['changeStaffRole', 'disableStaffAccount']) {
    const { op } = ops.get(id);
    assert.deepEqual(successes(op).sort(), ['200', '202'], id);
    assert.equal(refName(body(op, '200').schema), 'StaffAccount', id);
    assert.equal(refName(body(op, '202').schema), 'ApprovalAccepted', id);
  }
  const accepted = schema('ApprovalAccepted');
  assert.deepEqual(accepted.required, ['approval']);
  assert.equal(refName(accepted.properties.approval), 'Approval');
});

test('решение подтверждения отвечает 200 Approval', () => {
  for (const id of ['approveApproval', 'rejectApproval', 'cancelApproval']) {
    const { op } = ops.get(id);
    assert.deepEqual(successes(op), ['200'], id);
    assert.equal(refName(body(op, '200').schema), 'Approval', id);
  }
});

test('Set-Cookie объявлен у операций, которые ставят или стирают токен (P8)', () => {
  const withCookie = [
    'staffLogin',
    'staffVerifyMfa',
    'staffUnlock',
    'staffAcceptInvite',
    'staffRecoveryAck',
    'staffLogout',
  ];
  for (const [id, { op }] of ops) {
    const [status] = successes(op);
    const h = resolve(op.responses[status].headers?.['Set-Cookie'], 'headers');
    assert.equal(Boolean(h), withCookie.includes(id), id);
    if (h) {
      assert.equal(h.required, true, id);
      assert.match(h.example, /^__Host-wf_admin_session=/, id);
    }
  }
});

test('x-idempotency: no-store — ровно у операций с секретами, ответ с Cache-Control: no-store', () => {
  const noStore = ['staffMfaSetup', 'staffMfaConfirm', 'regenerateStaffRecoveryCodes'];
  for (const [id, { op }] of ops) {
    assert.equal(op['x-idempotency'], noStore.includes(id) ? 'no-store' : undefined, id);
  }
  for (const id of noStore) {
    const h = resolve(ops.get(id).op.responses['200'].headers?.['Cache-Control'], 'headers');
    assert.equal(h?.required, true, id);
    assert.equal(h.example, 'no-store', id);
  }
});

test('x-rate-limit staff_auth — у всех /v1/staff/auth/* и только у них', () => {
  for (const [id, { path, op }] of ops) {
    assert.equal(
      op['x-rate-limit'],
      path.startsWith('/v1/staff/auth/') ? 'staff_auth' : undefined,
      id,
    );
  }
});

test('коды ошибок операций (спека §7, reconcile §7)', () => {
  const codes = (id) => [...ops.get(id).op['x-error-codes']].sort();
  assert.deepEqual(codes('approveApproval'), [
    'platform.approval_closed',
    'platform.approval_not_found',
    'platform.approval_self',
    'platform.approval_stale',
    'staff.city_not_found',
    'staff.email_taken',
    'staff.last_owner',
  ]);
  assert.deepEqual(codes('resendStaffInvite'), [
    'staff.not_found',
    'staff.self_action',
    'staff.wrong_stage',
  ]);
  assert.deepEqual(codes('regenerateStaffRecoveryCodes'), [
    'staff.mfa_invalid',
    'staff.wrong_stage',
  ]);
  assert.deepEqual(codes('staffLogin'), ['staff.invalid_credentials']);
  assert.deepEqual(codes('staffAcceptInvite'), ['staff.invite_invalid']);
  assert.deepEqual(codes('changeStaffPassword'), ['staff.mfa_invalid', 'staff.password_invalid']);
  assert.deepEqual(codes('revokeStaffMySession'), ['staff.session_not_found']);
});

test('коды конвейера admin — в общих кодах; удалённые коды D18 не вводятся', () => {
  const common = doc['x-error-codes-common'];
  for (const c of [
    'auth.forbidden',
    'auth.csrf_failed',
    'auth.ip_not_allowed',
    'auth.reauth_required',
    'auth.setup_required',
  ]) {
    assert.ok(common.includes(c), c);
  }
  const all = JSON.stringify(doc);
  for (const gone of ['staff.locked', 'staff.invite_resend_too_soon', 'staff.reauth_required']) {
    assert.ok(!all.includes(`"${gone}"`), gone);
  }
});

test('Problem: attempts_left и retry_after — необязательные целые', () => {
  const p = schema('Problem');
  for (const f of ['attempts_left', 'retry_after']) {
    assert.equal(p.properties[f]?.type, 'integer', f);
    assert.ok(!p.required.includes(f), f);
  }
});

test('StaffMe (R15): права — объект PermissionGrant, сессия nullable', () => {
  const me = schema('StaffMe');
  assert.deepEqual(Object.keys(me.properties).sort(), [
    'city_ids',
    'display_name',
    'email',
    'id',
    'locale',
    'permissions',
    'reauth_required',
    'role',
    'session',
    'stage',
  ]);
  assert.equal(me.properties.permissions.type, 'object');
  assert.equal(refName(me.properties.permissions.additionalProperties), 'PermissionGrant');
  assert.deepEqual(schema('PermissionGrant').enum, ['allow', 'scoped', 'dual']);
  assert.equal(me.properties.session.nullable, true);
  // в mfa_pending приходят только stage и session (и пустые права): поля учётки необязательны
  for (const f of ['id', 'email', 'display_name', 'locale', 'role']) {
    assert.ok(!me.required.includes(f), f);
  }
});

test('StaffSession.user_agent, Approval.action — enum, AuditRecord с actor_label', () => {
  assert.ok(schema('StaffSession').properties.user_agent);
  const action = schema('ApprovalAction');
  assert.deepEqual(action.enum, [
    'staff.invite',
    'staff.role_change',
    'staff.access_reset',
    'staff.revoke_owner',
  ]);
  assert.equal(refName(schema('Approval').properties.action), 'ApprovalAction');
  const audit = schema('AuditRecord').properties;
  assert.ok(audit.actor_label && audit.ip, 'AuditRecord: actor_label и ip');
});

test('тела запросов: code | recovery_code, current_password | new_password, comment', () => {
  const factor = schema('StaffFactorRequest');
  assert.deepEqual(Object.keys(factor.properties).sort(), ['code', 'recovery_code']);
  assert.deepEqual([factor.minProperties, factor.maxProperties], [1, 1]);
  const pw = schema('StaffPasswordChange');
  assert.deepEqual(pw.required.sort(), ['current_password', 'new_password']);
  assert.deepEqual(schema('ApprovalReject').required, ['comment']);
  assert.ok(schema('ApprovalApprove').properties.comment);
  assert.ok(schema('StaffAcceptInviteRequest').properties.password);
});

test('listGeoCities: q, cursor и ids до 100 uuid (P28, M10)', () => {
  const { op } = ops.get('listGeoCities');
  const params = Object.fromEntries(
    (op.parameters ?? []).map((p) => resolve(p, 'parameters')).map((p) => [p.name, p]),
  );
  for (const name of ['q', 'cursor', 'ids']) assert.ok(params[name], name);
  const ids = params.ids.schema;
  assert.equal(ids.type, 'array');
  assert.equal(ids.maxItems, 100);
  assert.equal(ids.items.format, 'uuid');
  assert.match(op.description, /модератору — только его города/);
});

test('listApprovals: box incoming|mine обязателен, status и cursor', () => {
  const { op } = ops.get('listApprovals');
  const params = Object.fromEntries(
    (op.parameters ?? []).map((p) => resolve(p, 'parameters')).map((p) => [p.name, p]),
  );
  assert.equal(params.box.required, true);
  assert.deepEqual(params.box.schema.enum, ['incoming', 'mine']);
  assert.ok(params.status && params.cursor);
});

test('updateStaffMeLocale — PATCH /v1/staff/me/locale (R14)', () => {
  const { verb, path } = ops.get('updateStaffMeLocale');
  assert.deepEqual([verb, path], ['patch', '/v1/staff/me/locale']);
});
