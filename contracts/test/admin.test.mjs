// Контракт admin плана A (спека сотрудников §4.3–4.9, §5.3, §6, §8; план — R8, R13–R15, P8, P28;
// сверка reconcile §7–8) и плана B (geo 2/2, Task 7 — Приложения 1–3): пути, права, анонимность,
// форма ответов, cookie и заголовки — на собранном бандле. После Task 9a плана A и Task 7 плана B
// контракт заморожен: фронт и бэкенд работают по нему.
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
  'geo.countries.rules',
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
  // план B (geo 2/2), Приложение 1
  listGeoPlaces: ['get', '/v1/geo/places', 'geo', 'geo.places.search'],
  getGeoCity: ['get', '/v1/geo/cities/{cityId}', 'geo', 'geo.cities.read'],
  activateGeoCity: ['post', '/v1/geo/cities', 'geo', 'geo.cities.activate'],
  updateGeoCity: [
    'patch',
    '/v1/geo/cities/{cityId}',
    'geo',
    'handler',
    ['geo.cities.manage', 'geo.cities.rename'],
  ],
  linkGeoCity: ['post', '/v1/geo/cities/{cityId}/link', 'geo', 'geo.cities.manage'],
  listGeoDistricts: ['get', '/v1/geo/cities/{cityId}/districts', 'geo', 'geo.cities.read'],
  createGeoDistrict: ['post', '/v1/geo/cities/{cityId}/districts', 'geo', 'geo.districts.manage'],
  updateGeoDistrict: [
    'patch',
    '/v1/geo/cities/{cityId}/districts/{districtId}',
    'geo',
    'geo.districts.manage',
  ],
  listGeoCountries: ['get', '/v1/geo/countries', 'geo', 'geo.countries.read'],
  createGeoCountry: ['post', '/v1/geo/countries', 'geo', 'geo.countries.rules'],
  updateGeoCountry: [
    'patch',
    '/v1/geo/countries/{code}',
    'geo',
    'handler',
    ['geo.countries.manage', 'geo.countries.rules'],
  ],
  listGeoRegions: ['get', '/v1/geo/countries/{code}/regions', 'geo', 'geo.cities.read'],
  startGeoImport: ['post', '/v1/geo/imports', 'geo', 'geo.imports.run'],
  listGeoImports: ['get', '/v1/geo/imports', 'geo', 'geo.imports.read'],
  getGeoImport: ['get', '/v1/geo/imports/{importId}', 'geo', 'geo.imports.read'],
  getGeoImportJob: ['get', '/v1/geo/imports/jobs/{jobId}', 'geo', 'geo.imports.read'],
  cancelGeoImportJob: ['post', '/v1/geo/imports/jobs/{jobId}/cancel', 'geo', 'geo.imports.run'],
  listAppVersions: ['get', '/v1/app/versions', 'platform', 'platform.versions.read'],
  putAppVersions: ['put', '/v1/app/versions/{platform}', 'platform', 'platform.versions.manage'],
  listFeatureFlags: ['get', '/v1/flags', 'platform', 'platform.flags.read'],
  updateFeatureFlag: ['patch', '/v1/flags/{key}', 'platform', 'platform.flags.manage'],
};

test('54 операции: 33 плана A и 21 плана B — и только они', () => {
  assert.equal(Object.keys(expected).length, 54);
  assert.deepEqual([...ops.keys()].sort(), Object.keys(expected).sort());
  assert.equal(operations(doc).length, 54);
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

test('Problem: attempts_left — необязательное целое; retry_after в теле нет (только заголовок Retry-After)', () => {
  const p = schema('Problem');
  assert.equal(p.properties.attempts_left?.type, 'integer');
  assert.ok(!p.required.includes('attempts_left'));
  // сервер шлёт срок только заголовком (httpx.Problem.RetryAfter json:"-", решение 10a; ревью ветки, HTTP Minor 3)
  assert.equal(p.properties.retry_after, undefined);
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
    'geo.country_rules',
    'geo.country_create',
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

// ---- План B (geo 2/2), Task 7: Приложения 1–3 ----

const paramsOf = (op) =>
  Object.fromEntries(
    (op.parameters ?? []).map((p) => resolve(p, 'parameters')).map((p) => [p.name, p]),
  );

// operationId → [коды операции (Приложение 1, 3), успешные ответы: статус → схема тела]
const planB = {
  listGeoPlaces: [[], { 200: 'GeoPlacePage' }],
  listGeoCities: [[], { 200: 'GeoCityPage' }],
  getGeoCity: [['geo.city_not_found'], { 200: 'GeoCityDetails' }],
  activateGeoCity: [
    [
      'geo.city_already_active',
      'geo.country_not_enabled',
      'geo.place_not_found',
      'geo.region_not_found',
    ],
    { 200: 'GeoCityDetails' },
  ],
  updateGeoCity: [['geo.city_not_found', 'geo.slug_taken'], { 200: 'GeoCityDetails' }],
  linkGeoCity: [
    [
      'geo.city_already_active',
      'geo.city_already_linked',
      'geo.city_not_found',
      'geo.place_not_found',
    ],
    { 200: 'GeoCityDetails' },
  ],
  listGeoDistricts: [['geo.city_not_found'], { 200: 'GeoDistrictList' }],
  createGeoDistrict: [['geo.city_not_found', 'geo.district_name_taken'], { 200: 'GeoDistrict' }],
  updateGeoDistrict: [
    ['geo.district_name_taken', 'geo.district_not_found'],
    { 200: 'GeoDistrict' },
  ],
  listGeoCountries: [[], { 200: 'GeoCountryList' }],
  createGeoCountry: [['geo.country_exists'], { 202: 'ApprovalAccepted' }],
  updateGeoCountry: [['geo.country_not_found'], { 200: 'GeoCountry', 202: 'ApprovalAccepted' }],
  listGeoRegions: [['geo.country_not_found'], { 200: 'GeoRegionList' }],
  startGeoImport: [
    ['geo.country_not_found', 'geo.import_in_progress'],
    { 202: 'GeoImportStarted' },
  ],
  listGeoImports: [[], { 200: 'GeoImportPage' }],
  getGeoImport: [['geo.import_not_found'], { 200: 'GeoImportReport' }],
  getGeoImportJob: [['geo.import_job_not_found'], { 200: 'GeoImportJob' }],
  cancelGeoImportJob: [
    ['geo.import_job_not_cancellable', 'geo.import_job_not_found'],
    { 200: 'GeoImportJob' },
  ],
  listAppVersions: [[], { 200: 'AppVersionsList' }],
  putAppVersions: [[], { 200: 'AppVersions' }],
  listFeatureFlags: [[], { 200: 'FeatureFlagList' }],
  updateFeatureFlag: [
    ['platform.flag_city_not_found', 'platform.flag_not_found', 'platform.flag_stale'],
    { 200: 'FeatureFlag' },
  ],
};

for (const [id, [codes, ok]] of Object.entries(planB)) {
  test(`план B ${id}: коды ${codes.join(', ') || '—'}; ответы ${Object.keys(ok).join(', ')}`, () => {
    const { op } = ops.get(id);
    assert.deepEqual([...op['x-error-codes']].sort(), codes);
    const statuses = Object.keys(ok);
    assert.deepEqual(
      successes(op)
        .filter((s) => s < '400')
        .sort(),
      statuses,
    );
    for (const s of statuses) assert.equal(refName(body(op, s)?.schema), ok[s], `${id} ${s}`);
    // создание отвечает объектом, без 201 и Location (план, Task 7)
    assert.equal(op.responses['201'], undefined);
  });
}

test('план B: чтения — Cache-Control private, no-store; названия — Accept-Language, Vary, Content-Language', () => {
  const localized = new Set([
    'listGeoPlaces',
    'listGeoCities',
    'getGeoCity',
    'activateGeoCity',
    'updateGeoCity',
    'linkGeoCity',
    'listGeoCountries',
    'updateGeoCountry',
    'listGeoRegions',
    'getGeoImport',
  ]);
  for (const id of Object.keys(planB)) {
    const { verb, op } = ops.get(id);
    const status = successes(op).find((s) => s < '300');
    const headers = op.responses[status].headers ?? {};
    const h = (name) => resolve(headers[name], 'headers');
    if (verb === 'get') {
      assert.equal(h('Cache-Control')?.example, 'private, no-store', `${id}: Cache-Control`);
      assert.equal(h('Cache-Control').required, true, id);
    }
    const lang = localized.has(id);
    assert.equal(h('Vary')?.required === true, lang, `${id}: Vary`);
    assert.equal(Boolean(h('Content-Language')), lang, `${id}: Content-Language`);
    assert.equal(Boolean(paramsOf(op)['Accept-Language']), lang, `${id}: Accept-Language`);
  }
});

test('план B: параметры путей — uuid, код страны, jobId ≥ 1, платформа, ключ флага строкой (R6, I5)', () => {
  const p = (id, name) => paramsOf(ops.get(id).op)[name];
  for (const [id, name] of [
    ['getGeoCity', 'cityId'],
    ['updateGeoDistrict', 'districtId'],
    ['getGeoImport', 'importId'],
  ]) {
    assert.equal(p(id, name).in, 'path', `${id}.${name}`);
    assert.equal(p(id, name).schema.format, 'uuid', `${id}.${name}`);
  }
  assert.equal(p('updateGeoCountry', 'code').schema.pattern, '^[A-Z]{2}$');
  const job = p('cancelGeoImportJob', 'jobId').schema;
  assert.deepEqual([job.type, job.format, job.minimum], ['integer', 'int64', 1]);
  const platform = resolve(p('putAppVersions', 'platform').schema, 'schemas');
  assert.deepEqual(platform.enum, ['ios', 'android']);
  assert.equal(platform['x-extensible-enum'], true);
  // ключ флага в пути — не enum: иначе 404 platform.flag_not_found недостижим (валидатор отвечает 400)
  const key = p('updateFeatureFlag', 'key').schema;
  assert.equal(key.$ref, undefined);
  assert.equal(key.enum, undefined);
  assert.deepEqual([key.type, key.pattern, key.maxLength], ['string', '^[a-z][a-z0-9_]*$', 64]);
});

test('план B: listGeoCities расширен — status (массив, form, explode), country, region_id', () => {
  const p = paramsOf(ops.get('listGeoCities').op);
  assert.deepEqual(
    [p.status.style, p.status.explode, p.status.schema.type],
    ['form', true, 'array'],
  );
  assert.equal(refName(p.status.schema.items), 'CityStatus');
  assert.equal(p.country.schema.pattern, '^[A-Z]{2}$');
  assert.equal(p.region_id.schema.format, 'uuid');
  for (const name of ['q', 'ids', 'cursor', 'limit']) assert.ok(p[name], name);
  for (const name of ['status', 'country', 'region_id']) assert.equal(p[name].required, false);
  const city = schema('GeoCity');
  for (const f of ['region_id', 'country_enabled', 'geoname_id', 'population', 'districts_count']) {
    assert.ok(city.properties[f], `GeoCity.${f}`);
  }
  assert.equal(city.properties.region_id.nullable, true);
  assert.equal(city.properties.geoname_id.nullable, true);
});

test('план B: listGeoImports — country и job_id; listGeoPlaces — q и country', () => {
  const imports = paramsOf(ops.get('listGeoImports').op);
  assert.equal(imports.country.schema.pattern, '^[A-Z]{2}$');
  assert.deepEqual(
    [imports.job_id.schema.type, imports.job_id.schema.format],
    ['integer', 'int64'],
  );
  assert.ok(imports.cursor && imports.limit);
  const places = paramsOf(ops.get('listGeoPlaces').op);
  assert.ok(places.q && places.country && places.cursor && places.limit);
});

test('план B: FeatureFlagKey — открытый enum ключей сида 0010, только в ответе (R6)', () => {
  const key = schema('FeatureFlagKey');
  assert.deepEqual(key.enum, [
    'subscriptions',
    'economy',
    'lootboxes',
    'predictions',
    'competitions',
    'city_tables',
    'challenges',
    'match_comments',
    'payment_flags',
    'ads',
    'gk_rotation',
  ]);
  assert.equal(key['x-extensible-enum'], true);
  assert.equal(refName(schema('FeatureFlag').properties.key), 'FeatureFlagKey');
  // в запросах ключа нет: путь — строка, тело правки — без key
  assert.equal(schema('FeatureFlagPatch').properties.key, undefined);
  assert.ok(!JSON.stringify(ops.get('updateFeatureFlag').op.parameters).includes('FeatureFlagKey'));
});

test('план B: FeatureFlagPatch — version обязательна, города до 1000; 409 platform.flag_stale в описании', () => {
  const patch = schema('FeatureFlagPatch');
  assert.deepEqual(patch.required, ['version']);
  assert.equal(patch.additionalProperties, false);
  assert.equal(patch.properties.enabled_city_ids.maxItems, 1000);
  assert.equal(patch.properties.enabled_city_ids.items.format, 'uuid');
  assert.match(ops.get('updateFeatureFlag').op.description, /platform\.flag_stale/);
});

test('план B: тела правок — minProperties 1 без лишних полей; страна без default_locale и code', () => {
  for (const name of ['GeoCityPatch', 'GeoDistrictPatch', 'GeoCountryPatch']) {
    const s = schema(name);
    assert.equal(s.minProperties, 1, name);
    assert.equal(s.additionalProperties, false, name);
  }
  const country = schema('GeoCountryPatch').properties;
  assert.equal(country.default_locale, undefined);
  assert.equal(country.code, undefined);
  const create = schema('GeoCountryCreate');
  for (const f of [
    'code',
    'currency',
    'default_locale',
    'phone_prefix',
    'week_starts_on',
    'min_signup_age',
    'age_of_majority',
    'names',
  ]) {
    assert.ok(create.required.includes(f), `GeoCountryCreate.${f}`);
  }
  assert.ok(!create.required.includes('is_enabled'));
  assert.deepEqual(schema('GeoCityActivateRequest').required, ['geoname_id']);
  assert.deepEqual(schema('GeoImportStartRequest').required, ['country']);
  assert.equal(schema('GeoImportStartRequest').properties.country.pattern, '^[A-Z]{2}$');
});

test('план B: AppVersionsPut — store_url ^https://, версии MAJOR.MINOR.PATCH', () => {
  const put = schema('AppVersionsPut');
  assert.deepEqual(put.required.sort(), ['min_version', 'recommended_version', 'store_url']);
  assert.equal(put.properties.store_url.pattern, '^https://');
  assert.match(put.properties.store_url.description, /нижнем регистре/);
  assert.match(put.properties.min_version.pattern, /^\^\(0\|\[1-9\]/);
});

test('план B: GeoImportJob.state и GeoImport.status — открытые enum', () => {
  const job = schema('GeoImportJob').properties.state;
  assert.deepEqual(resolve(job, 'schemas').enum, [
    'queued',
    'running',
    'completed',
    'failed',
    'cancelled',
  ]);
  const st = resolve(schema('GeoImport').properties.status, 'schemas');
  assert.deepEqual(st.enum, ['running', 'succeeded', 'failed']);
  assert.equal(schema('GeoImport').properties.started_by_label.nullable, true);
});

test('план B: Problem.job_id — необязательное int64; 409 startGeoImport с примером job_id (R26)', () => {
  const p = schema('Problem');
  assert.deepEqual([p.properties.job_id?.type, p.properties.job_id?.format], ['integer', 'int64']);
  assert.ok(!p.required.includes('job_id'));
  const conflict = ops.get('startGeoImport').op.responses['409'];
  const media = resolve(conflict, 'responses').content['application/problem+json'];
  assert.equal(refName(media.schema), 'Problem');
  assert.equal(media.example.code, 'geo.import_in_progress');
  assert.equal(typeof media.example.job_id, 'number');
});

test('план B: описания фиксируют поведение, на которое опираются экраны', () => {
  const d = (id) => ops.get(id).op.description;
  assert.match(d('updateGeoCountry'), /вдвоём/);
  assert.match(d('updateGeoCountry'), /202/);
  assert.match(d('createGeoCountry'), /второй сотрудник/i);
  assert.match(d('updateGeoCity'), /403/);
  assert.match(d('updateGeoCity'), /последняя запись выигрывает/i);
  assert.match(d('startGeoImport'), /в очереди/);
  assert.match(d('cancelGeoImportJob'), /воркер/);
  assert.match(d('listGeoImports'), /started_by_label/);
  // K6 (раунд 1 ревью G4): повторная отмена уже отменённой задачи — 200 без второй записи журнала
  assert.match(d('cancelGeoImportJob'), /уже отменённ[а-я]* задач[а-я]* — 200/);
  assert.match(d('cancelGeoImportJob'), /второй записи журнала нет/);
});

// Ревью G3 (⚠️): approve описывает отказ исполнителей geo — наружу только platform.approval_stale (R4,
// Task 13): бизнес-кодов geo.* у approve нет, x-error-codes не растёт.
test('approveApproval: отказ исполнителей geo.country_rules и geo.country_create — 409 approval_stale', () => {
  const { op } = ops.get('approveApproval');
  // пункты списка кодов — по «- » в начале строки (пункт занимает несколько строк)
  const lines = op.description
    .split(/\n\s*- /)
    .filter((l) => /geo\.country_(rules|create)/.test(l));
  assert.ok(lines.length > 0, 'описание не называет действия geo');
  assert.ok(
    lines.some((l) => l.includes('platform.approval_stale')),
    'отказ исполнителя geo — 409 platform.approval_stale',
  );
  assert.ok(
    !op['x-error-codes'].some((c) => c.startsWith('geo.')),
    'бизнес-кодов geo у approve нет',
  );
});
