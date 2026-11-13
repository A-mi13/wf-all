// Публичные операции identity (спека identity §6, план I-1): строка на операцию — метод, путь, тег, анонимность,
// Idempotency-Key, класс x-rate-limit, коды identity.*, статус и схема успеха, Cache-Control — на собранном бандле.
// Строки повторяют таблицы спеки §6.3 (ревьюер сверяет их со спекой, а не с YAML); отступления — решения плана R.
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { parse } from 'yaml';
import { operations } from '../src/conventions.mjs';

const doc = parse(await readFile(new URL('../openapi/public.yaml', import.meta.url), 'utf8'));
const ops = new Map(operations(doc).map((o) => [o.op.operationId, o]));
const KEY = '#/components/parameters/IdempotencyKey';
const deref = (kind, n) => (n?.$ref ? doc.components[kind][n.$ref.split('/').at(-1)] : n);
const schema = (name) => doc.components.schemas[name];
const refName = (s) => s?.$ref?.split('/').at(-1);

const NO_STORE = 'no-store';
const PRIVATE = 'private, no-store';

// [метод, путь, анонимная, класс x-rate-limit | undefined (default), коды identity.* без префикса, статус успеха,
//  схема успеха | null (без тела), Cache-Control | null (204 — без заголовка)]
const expected = {
  // «Вход и регистрация»
  checkNickname: [
    'get',
    '/v1/nicknames/availability',
    true,
    'auth_check',
    [],
    '200',
    'NicknameAvailability',
    PRIVATE,
  ],
  startSignup: [
    'post',
    '/v1/auth/signup',
    true,
    'auth_send',
    [
      'documents_outdated',
      'device_required',
      'too_young',
      'city_not_found',
      'password_weak',
      'nickname_invalid',
      'nickname_taken',
      'email_disposable',
      'device_signup_limit',
      'device_untrusted',
    ],
    '202',
    'CodeSent',
    PRIVATE,
  ],
  // R10: device_required сверх таблицы (§6.2.3 перечисляет confirmSignup среди операций с обязательным device)
  confirmSignup: [
    'post',
    '/v1/auth/signup/confirm',
    true,
    'auth',
    [
      'code_invalid',
      'code_expired',
      'nickname_taken',
      'email_taken',
      'device_signup_limit',
      'device_required',
    ],
    '201',
    'AuthResult',
    NO_STORE,
  ],
  resendAuthCode: [
    'post',
    '/v1/auth/codes/{codeId}/resend',
    true,
    'auth_send',
    ['resend_too_soon', 'code_expired'],
    '202',
    'CodeSent',
    PRIVATE,
  ],
  login: [
    'post',
    '/v1/auth/login',
    true,
    'auth',
    ['invalid_credentials', 'account_blocked', 'device_untrusted', 'device_required'],
    '200',
    'AuthResult',
    NO_STORE,
  ],
  completeMfa: [
    'post',
    '/v1/auth/mfa',
    true,
    'auth',
    ['mfa_invalid', 'mfa_expired', 'passkey_invalid', 'device_required'],
    '200',
    'AuthResult',
    NO_STORE,
  ],
  startPasswordReset: [
    'post',
    '/v1/auth/password-reset',
    true,
    'auth_send',
    [],
    '202',
    'CodeSent',
    PRIVATE,
  ],
  confirmPasswordReset: [
    'post',
    '/v1/auth/password-reset/confirm',
    true,
    'auth',
    [
      'code_invalid',
      'code_expired',
      'password_weak',
      'mfa_required',
      'mfa_invalid',
      'device_required',
    ],
    '200',
    'AuthResult',
    NO_STORE,
  ],
  refreshSession: [
    'post',
    '/v1/auth/refresh',
    true,
    'auth_refresh',
    ['refresh_invalid'],
    '200',
    'TokenPair',
    NO_STORE,
  ],
  cancelDeletion: [
    'post',
    '/v1/auth/deletion/cancel',
    true,
    'auth',
    ['restore_token_invalid', 'device_required'],
    '200',
    'AuthResult',
    NO_STORE,
  ],
  getLegalDocuments: [
    'get',
    '/v1/legal/documents',
    true,
    undefined,
    [],
    '200',
    'LegalDocumentList',
    'public, max-age=300',
  ],
  createDeviceChallenge: [
    'post',
    '/v1/devices/challenges',
    true,
    'auth',
    [],
    '201',
    'DeviceChallenge',
    NO_STORE,
  ],
  logout: ['post', '/v1/auth/logout', false, undefined, [], '204', null, null],
  // «Аккаунт»
  getMe: ['get', '/v1/me', false, undefined, [], '200', 'Me', PRIVATE],
  updateMe: ['patch', '/v1/me', false, undefined, [], '200', 'Me', PRIVATE],
  changeNickname: [
    'patch',
    '/v1/me/nickname',
    false,
    'account_secure',
    ['nickname_invalid', 'nickname_taken', 'nickname_change_too_soon'],
    '200',
    'Me',
    PRIVATE,
  ],
  listMySessions: ['get', '/v1/me/sessions', false, undefined, [], '200', 'SessionList', PRIVATE],
  revokeMySession: [
    'delete',
    '/v1/me/sessions/{sessionId}',
    false,
    undefined,
    ['session_not_found'],
    '204',
    null,
    null,
  ],
  revokeOtherSessions: [
    'post',
    '/v1/me/sessions/revoke-others',
    false,
    undefined,
    [],
    '200',
    'RevokedCount',
    PRIVATE,
  ],
  changePassword: [
    'post',
    '/v1/me/password',
    false,
    'account_secure',
    ['reauth_required', 'password_weak'],
    '204',
    null,
    null,
  ],
  startEmailChange: [
    'post',
    '/v1/me/email-change',
    false,
    'auth_send',
    ['reauth_required', 'email_same', 'email_disposable'],
    '202',
    'CodeSent',
    PRIVATE,
  ],
  confirmEmailChange: [
    'post',
    '/v1/me/email-change/confirm',
    false,
    'auth',
    ['code_invalid', 'code_expired', 'email_taken'],
    '200',
    'Me',
    PRIVATE,
  ],
  acceptDocuments: [
    'post',
    '/v1/me/documents',
    false,
    undefined,
    ['documents_outdated'],
    '200',
    'Me',
    PRIVATE,
  ],
  deleteAccount: [
    'post',
    '/v1/me/delete',
    false,
    'account_secure',
    ['reauth_required', 'confirm_mismatch'],
    '200',
    'DeletionScheduled',
    PRIVATE,
  ],
};

const MUTATING = new Set(['post', 'put', 'patch', 'delete']);
const header = (h) => deref('headers', h);

for (const [id, [verb, path, anon, rate, codes, status, body, cache]] of Object.entries(expected)) {
  test(`${id}: ${verb.toUpperCase()} ${path}, тег identity, ${anon ? 'анонимная' : 'с замком'}`, () => {
    const o = ops.get(id);
    assert.ok(o, `нет операции ${id}`);
    assert.equal(o.verb, verb);
    assert.equal(o.path, path);
    assert.deepEqual(o.op.tags, ['identity']);
    assert.equal(Array.isArray(o.op.security) && o.op.security.length === 0, anon, 'анонимность');
    const hasKey = (o.op.parameters ?? []).some((p) => p.$ref === KEY);
    assert.equal(hasKey, !anon && MUTATING.has(verb), 'Idempotency-Key — у мутаций с замком');
    assert.equal(o.op['x-rate-limit'], rate, 'x-rate-limit');
    assert.deepEqual(
      [...o.op['x-error-codes']].sort(),
      codes.map((c) => `identity.${c}`).sort(),
      'x-error-codes',
    );
  });

  test(`${id}: успех ${status}${body ? ` ${body}` : ' без тела'}, Cache-Control ${cache ?? 'нет'}`, () => {
    const op = ops.get(id).op;
    const r = deref('responses', op.responses[status]);
    assert.ok(r, `нет ответа ${status}`);
    const success = Object.keys(op.responses).filter((s) => /^2/.test(s));
    assert.deepEqual(success, [status], 'ровно один успешный ответ');
    assert.equal(refName(r.content?.['application/json']?.schema) ?? null, body);
    const cc = header(r.headers?.['Cache-Control']);
    assert.equal(cc?.example ?? null, cache);
    if (cache) assert.equal(cc.required, true);
  });
}

test('все операции тега identity — в таблице теста', () => {
  const tagged = [...ops.values()]
    .filter((o) => o.op.tags?.includes('identity'))
    .map((o) => o.op.operationId);
  assert.deepEqual(
    tagged.filter((id) => !(id in expected)),
    [],
  );
});

test('x-anonymous-mutations — ровно анонимные POST таблицы, у каждой security: [] и обоснование', () => {
  const want = Object.entries(expected)
    .filter(([, [verb, , anon]]) => anon && verb !== 'get')
    .map(([id]) => id)
    .sort();
  assert.equal(want.length, 10, 'Task 2: 10 анонимных мутаций (Task 3 доведёт до 16)');
  assert.deepEqual([...doc['x-anonymous-mutations']].sort(), want);
  for (const id of want) {
    assert.match(
      ops.get(id).op.description,
      /Анонимная\s+операция:/,
      `${id}: обоснование анонимности в description`,
    );
  }
  assert.ok(!doc['x-anonymous-mutations'].includes('logout'), 'logout — с замком');
});

// §6.2.3, К5: у шести операций Task 2 поле device ($ref DeviceInfo, необязательное в схеме) и правило в описании.
const DEVICE = [
  'startSignup',
  'confirmSignup',
  'login',
  'completeMfa',
  'confirmPasswordReset',
  'cancelDeletion',
];

test('device: поле DeviceInfo у шести операций, правило device_required в описаниях и info.description', () => {
  for (const id of DEVICE) {
    const op = ops.get(id).op;
    const req = deref('schemas', op.requestBody?.content?.['application/json']?.schema);
    assert.equal(refName(req.properties?.device), 'DeviceInfo', `${id}: поле device`);
    assert.ok(!(req.required ?? []).includes('device'), `${id}: device необязателен в схеме`);
    assert.match(op.description, /identity\.device_required/, `${id}: правило в description`);
  }
  assert.match(doc.info.description, /`device`/);
  assert.match(doc.info.description, /identity\.device_required/);
});

// R6: ответ с токенами или секретами — Cache-Control no-store; мутация с замком с такими полями — x-idempotency.
const SECRET = new Set([
  'access_token',
  'refresh_token',
  'mfa_token',
  'signup_token',
  'restore_token',
  'secret',
  'otpauth_uri',
  'recovery_codes',
]);

function hasSecret(s, seen = new Set()) {
  if (!s || typeof s !== 'object') return false;
  if (s.$ref) {
    const name = refName(s);
    if (seen.has(name)) return false;
    seen.add(name);
    return hasSecret(schema(name), seen);
  }
  for (const [k, v] of Object.entries(s.properties ?? {})) {
    if (SECRET.has(k) || hasSecret(v, seen)) return true;
  }
  return (
    ['allOf', 'oneOf', 'anyOf'].some((k) => (s[k] ?? []).some((p) => hasSecret(p, seen))) ||
    hasSecret(s.items, seen)
  );
}

test('секреты и кэш: ответ с токенами — Cache-Control no-store, у мутации с замком — x-idempotency', () => {
  let checked = 0;
  for (const { op } of ops.values()) {
    for (const [status, raw] of Object.entries(op.responses ?? {})) {
      if (!/^2/.test(status)) continue;
      const r = deref('responses', raw);
      if (!hasSecret(r.content?.['application/json']?.schema)) continue;
      checked++;
      assert.equal(
        header(r.headers?.['Cache-Control'])?.example,
        NO_STORE,
        `${op.operationId}: Cache-Control`,
      );
      const keyed = (op.parameters ?? []).some((p) => p.$ref === KEY);
      if (keyed) assert.equal(op['x-idempotency'], 'no-store', `${op.operationId}: x-idempotency`);
    }
  }
  assert.ok(checked >= 6, `ответов с токенами ${checked} — страж проверяет вхолостую`);
});

// Ошибки с полями (§6.5, R3): пример Problem в ответе операции для каждого кода с полем.
const FIELDS = {
  startSignup: [['422', 'identity.password_weak', 'reasons']],
  confirmSignup: [['400', 'identity.code_invalid', 'attempts_left']],
  resendAuthCode: [['429', 'identity.resend_too_soon', null]],
  login: [['403', 'identity.account_blocked', 'blocked_until']],
  completeMfa: [['400', 'identity.mfa_invalid', 'attempts_left']],
  confirmPasswordReset: [
    ['400', 'identity.mfa_required', 'methods'],
    ['400', 'identity.code_invalid', 'attempts_left'],
    ['400', 'identity.mfa_invalid', 'attempts_left'],
    ['422', 'identity.password_weak', 'reasons'],
  ],
  changeNickname: [['409', 'identity.nickname_change_too_soon', 'available_at']],
  changePassword: [['422', 'identity.password_weak', 'reasons']],
  confirmEmailChange: [['400', 'identity.code_invalid', 'attempts_left']],
};

const problemExamples = (op, status) => {
  const r = deref('responses', op.responses[status]);
  return Object.values(r?.content?.['application/problem+json']?.examples ?? {}).map(
    (e) => e.value,
  );
};

for (const [id, rows] of Object.entries(FIELDS)) {
  test(`${id}: пример Problem для кодов с полями`, () => {
    const op = ops.get(id).op;
    for (const [status, code, field] of rows) {
      const ex = problemExamples(op, status).find((v) => v?.code === code);
      assert.ok(ex, `${status} ${code}: нет примера`);
      assert.equal(String(ex.status), status);
      if (field) assert.ok(ex[field] !== undefined, `${code}: в примере нет ${field}`);
    }
  });
}

test('password_weak: reasons — ровно одно из too_short | common | too_long (R3)', () => {
  for (const id of ['startSignup', 'confirmPasswordReset', 'changePassword']) {
    const ex = problemExamples(ops.get(id).op, '422').find(
      (v) => v.code === 'identity.password_weak',
    );
    assert.equal(ex.reasons.length, 1);
    assert.ok(['too_short', 'common', 'too_long'].includes(ex.reasons[0]));
  }
  const d = doc.components.schemas.Problem.properties.reasons.description;
  for (const v of ['too_short', 'common', 'too_long']) assert.match(d, new RegExp(v));
});

test('confirmPasswordReset: methods у mfa_required — без passkey (§3.4, m4)', () => {
  const ex = problemExamples(ops.get('confirmPasswordReset').op, '400').find(
    (v) => v.code === 'identity.mfa_required',
  );
  assert.ok(!ex.methods.includes('passkey'));
});

test('resendAuthCode: 429 с заголовком Retry-After', () => {
  const r = deref('responses', ops.get('resendAuthCode').op.responses['429']);
  assert.equal(header(r.headers?.['Retry-After'])?.schema?.type, 'integer');
});

// §9.1: ответ не раскрывает существование аккаунта — описание фиксирует одинаковый ответ.
test('перечисление аккаунтов: одинаковые ответы зафиксированы в описаниях', () => {
  for (const id of ['startSignup', 'startPasswordReset', 'resendAuthCode']) {
    assert.match(ops.get(id).op.description, /не\s+зависит\s+от\s+того,\s+есть\s+ли\s+аккаунт/, id);
  }
  assert.match(ops.get('login').op.description, /если\s+аккаунта\s+нет/);
});

test('свежий вход: reauth_required и reauthenticate в описаниях', () => {
  for (const id of ['changePassword', 'startEmailChange', 'deleteAccount']) {
    const d = ops.get(id).op.description;
    assert.match(d, /свежий вход/, id);
    assert.match(d, /reauthenticate/, id);
  }
});

// Реестр схем Task 2 (план I-1, Приложение 2): владелец — эта задача, Task 3–4 ссылаются.
const SCHEMAS = [
  'AuthResult',
  'AuthStatus',
  'TokenPair',
  'AuthUser',
  'MfaChallenge',
  'MfaMethod',
  'SocialSignupPending',
  'DeletionPending',
  'CodeSent',
  'DeviceInfo',
  'DeviceIntegrity',
  'DeviceIntegrityKind',
  'DeviceChallenge',
  'WebAuthnOptions',
  'WebAuthnCredential',
  'PasskeyAssertion',
  'NicknameAvailability',
  'NicknameUnavailableReason',
  'LegalDocumentKind',
  'LegalDocument',
  'LegalDocumentList',
  'AcceptedDocument',
  'Locale',
  'AccountStatus',
  'Me',
  'MeRestriction',
  'MeDocument',
  'MeUpdate',
  'Session',
  'ClientKind',
  'AuthMethod',
  'SessionList',
  'RevokedCount',
  'SignupStartRequest',
  'SignupConfirmRequest',
  'LoginRequest',
  'MfaCompleteRequest',
  'PasswordResetStartRequest',
  'PasswordResetConfirmRequest',
  'RefreshRequest',
  'DeletionCancelRequest',
  'NicknameChangeRequest',
  'PasswordChangeRequest',
  'EmailChangeStartRequest',
  'EmailChangeConfirmRequest',
  'DocumentsAcceptRequest',
  'DeletionRequest',
  'DeletionScheduled',
];

test('реестр схем Task 2 — все схемы на месте', () => {
  assert.deepEqual(
    SCHEMAS.filter((n) => !schema(n)),
    [],
  );
});

test('перечисления Task 2 — значения спеки, открытые', () => {
  const enums = {
    AuthStatus: ['authenticated', 'mfa_required', 'signup_required', 'deletion_pending'],
    MfaMethod: ['totp', 'recovery_code', 'passkey'],
    DeviceIntegrityKind: ['app_attest_attestation', 'app_attest_assertion', 'play_integrity'],
    NicknameUnavailableReason: ['invalid', 'taken', 'reserved'],
    LegalDocumentKind: ['terms', 'privacy'],
    Locale: ['ru', 'en'],
    AccountStatus: ['active', 'suspended', 'banned', 'pending_deletion', 'deleted'],
    ClientKind: ['mobile', 'web'],
    AuthMethod: ['password', 'apple', 'google', 'passkey'],
  };
  for (const [name, values] of Object.entries(enums)) {
    assert.deepEqual(schema(name).enum, values, name);
    assert.equal(schema(name)['x-extensible-enum'], true, name);
  }
});

test('MeUpdate: minProperties 1, additionalProperties false; MfaCompleteRequest — одна схема (R11)', () => {
  assert.equal(schema('MeUpdate').minProperties, 1);
  assert.equal(schema('MeUpdate').additionalProperties, false);
  const m = schema('MfaCompleteRequest');
  assert.equal(m.oneOf, undefined);
  assert.deepEqual(m.required, ['mfa_token']);
  assert.match(m.description, /ровно одно/);
});

test('accepted_documents — minItems 1, правило «все текущие» — documents_outdated', () => {
  for (const name of ['SignupStartRequest', 'DocumentsAcceptRequest']) {
    const a = schema(name).properties.accepted_documents;
    assert.equal(a.minItems, 1, name);
    assert.equal(refName(a.items), 'AcceptedDocument');
  }
});

test('info.description — раздел «Вход и токены»', () => {
  const d = doc.info.description;
  assert.match(d, /Вход и токены/);
  assert.match(d, /refreshSession/);
  assert.match(d, /Keychain/);
  assert.match(d, /identity\.restricted/);
});
