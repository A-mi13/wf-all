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
  // Task 3: способы входа, 2FA ключом, соцвход, вход ключом (§6.3 «Вход и регистрация»)
  getAuthMethods: [
    'get',
    '/v1/auth/methods',
    true,
    undefined,
    [],
    '200',
    'AuthMethods',
    'public, max-age=60',
  ],
  startMfaPasskey: [
    'post',
    '/v1/auth/mfa/passkey-options',
    true,
    'auth',
    ['mfa_expired', 'passkey_none'],
    '200',
    'WebAuthnOptions',
    NO_STORE,
  ],
  createSocialNonce: [
    'post',
    '/v1/auth/social/nonces',
    true,
    'auth',
    [],
    '201',
    'SocialNonce',
    NO_STORE,
  ],
  // device_required — правило §6.2.3 (абзац под таблицей §6.3)
  socialLogin: [
    'post',
    '/v1/auth/social',
    true,
    'auth',
    [
      'social_token_invalid',
      'social_email_unverified',
      'social_email_missing',
      'account_blocked',
      'device_untrusted',
      'device_required',
    ],
    '200',
    'AuthResult',
    NO_STORE,
  ],
  completeSocialSignup: [
    'post',
    '/v1/auth/social/signup',
    true,
    'auth',
    [
      'signup_token_invalid',
      'documents_outdated',
      'device_required',
      'too_young',
      'city_not_found',
      'nickname_invalid',
      'nickname_taken',
      'email_taken',
      'device_signup_limit',
      'device_untrusted',
    ],
    '201',
    'AuthResult',
    NO_STORE,
  ],
  startPasskeyLogin: [
    'post',
    '/v1/auth/passkey/options',
    true,
    'auth',
    [],
    '200',
    'WebAuthnOptions',
    NO_STORE,
  ],
  passkeyLogin: [
    'post',
    '/v1/auth/passkey',
    true,
    'auth',
    ['passkey_invalid', 'account_blocked', 'device_required', 'device_untrusted'],
    '200',
    'AuthResult',
    NO_STORE,
  ],
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
  // Task 3: свежий вход, способы входа, 2FA, ключи доступа, сброс 2FA (§6.3 «Аккаунт»)
  reauthenticate: [
    'post',
    '/v1/me/reauth',
    false,
    'account_secure',
    ['reauth_invalid', 'reauth_method_not_allowed'],
    '200',
    'ReauthResult',
    PRIVATE,
  ],
  startReauthPasskey: [
    'post',
    '/v1/me/reauth/passkey-options',
    false,
    'account_secure',
    ['passkey_none'],
    '200',
    'WebAuthnOptions',
    NO_STORE,
  ],
  listMyAuthMethods: [
    'get',
    '/v1/me/auth-methods',
    false,
    undefined,
    [],
    '200',
    'MyAuthMethods',
    PRIVATE,
  ],
  linkIdentity: [
    'post',
    '/v1/me/identities',
    false,
    'account_secure',
    ['reauth_required', 'social_token_invalid', 'identity_taken', 'identity_exists'],
    '201',
    'LinkedIdentity',
    PRIVATE,
  ],
  unlinkIdentity: [
    'delete',
    '/v1/me/identities/{identityId}',
    false,
    'account_secure',
    ['reauth_required', 'identity_not_found', 'last_method'],
    '204',
    null,
    null,
  ],
  getMyMfa: ['get', '/v1/me/mfa', false, undefined, [], '200', 'MfaStatus', PRIVATE],
  setupTotp: [
    'post',
    '/v1/me/mfa/totp/setup',
    false,
    'account_secure',
    ['reauth_required', 'mfa_already_enabled'],
    '200',
    'TotpSetup',
    NO_STORE,
  ],
  confirmTotp: [
    'post',
    '/v1/me/mfa/totp/confirm',
    false,
    'account_secure',
    ['mfa_setup_missing', 'mfa_invalid'],
    '200',
    'RecoveryCodes',
    NO_STORE,
  ],
  disableTotp: [
    'delete',
    '/v1/me/mfa/totp',
    false,
    'account_secure',
    ['reauth_required', 'mfa_not_enabled'],
    '204',
    null,
    null,
  ],
  regenerateRecoveryCodes: [
    'post',
    '/v1/me/mfa/recovery-codes',
    false,
    'account_secure',
    ['reauth_required', 'mfa_not_enabled'],
    '200',
    'RecoveryCodes',
    NO_STORE,
  ],
  startPasskeyRegistration: [
    'post',
    '/v1/me/passkeys/registration-options',
    false,
    'account_secure',
    ['reauth_required', 'passkey_limit'],
    '200',
    'WebAuthnOptions',
    NO_STORE,
  ],
  registerPasskey: [
    'post',
    '/v1/me/passkeys',
    false,
    'account_secure',
    ['passkey_invalid', 'passkey_exists'],
    '201',
    'Passkey',
    PRIVATE,
  ],
  renamePasskey: [
    'patch',
    '/v1/me/passkeys/{passkeyId}',
    false,
    undefined,
    ['passkey_not_found'],
    '200',
    'Passkey',
    PRIVATE,
  ],
  deletePasskey: [
    'delete',
    '/v1/me/passkeys/{passkeyId}',
    false,
    'account_secure',
    ['reauth_required', 'passkey_not_found', 'last_method'],
    '204',
    null,
    null,
  ],
  cancelMyMfaReset: [
    'delete',
    '/v1/me/mfa-reset',
    false,
    'account_secure',
    ['mfa_reset_not_found'],
    '204',
    null,
    null,
  ],
  // Task 4: капитанство (§6.3 «Капитанство», §3.12)
  getMyCaptaincy: ['get', '/v1/me/captaincy', false, undefined, [], '200', 'Captaincy', PRIVATE],
  listMyCaptainApplications: [
    'get',
    '/v1/me/captain-applications',
    false,
    undefined,
    [],
    '200',
    'CaptainApplicationPage',
    PRIVATE,
  ],
  submitCaptainApplication: [
    'post',
    '/v1/captain-applications',
    false,
    undefined,
    ['captain_ineligible', 'application_exists', 'application_cooldown'],
    '201',
    'CaptainApplication',
    PRIVATE,
  ],
  withdrawCaptainApplication: [
    'post',
    '/v1/captain-applications/{applicationId}/withdraw',
    false,
    undefined,
    ['application_not_found', 'application_closed'],
    '200',
    'CaptainApplication',
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
  // §6.3: 20 входа, 26 аккаунта, 4 капитанства
  assert.equal(tagged.length, 50);
  assert.equal(Object.keys(expected).length, 50);
});

test('x-anonymous-mutations — ровно анонимные POST таблицы, у каждой security: [] и обоснование', () => {
  const want = Object.entries(expected)
    .filter(([, [verb, , anon]]) => anon && verb !== 'get')
    .map(([id]) => id)
    .sort();
  assert.equal(
    want.length,
    16,
    '16 анонимных мутаций §6.3 (все POST «Вход и регистрация», кроме logout)',
  );
  assert.deepEqual([...doc['x-anonymous-mutations']].sort(), want);
  const anonMutating = [...ops.values()]
    .filter((o) => MUTATING.has(o.verb) && Array.isArray(o.op.security) && !o.op.security.length)
    .map((o) => o.op.operationId)
    .sort();
  assert.deepEqual(anonMutating, want, 'мутации с security: [] — ровно список');
  for (const id of want) {
    assert.match(
      ops.get(id).op.description,
      /Анонимная\s+операция:/,
      `${id}: обоснование анонимности в description`,
    );
  }
  assert.ok(!doc['x-anonymous-mutations'].includes('logout'), 'logout — с замком');
});

// §6.2.3, К5: у девяти операций поле device ($ref DeviceInfo, необязательное в схеме), правило в описании и код.
const DEVICE = [
  'startSignup',
  'confirmSignup',
  'login',
  'completeMfa',
  'confirmPasswordReset',
  'cancelDeletion',
  'socialLogin',
  'completeSocialSignup',
  'passkeyLogin',
];

test('device: поле DeviceInfo у девяти операций §6.2.3, правило device_required в описаниях и info.description', () => {
  for (const id of DEVICE) {
    const op = ops.get(id).op;
    const req = deref('schemas', op.requestBody?.content?.['application/json']?.schema);
    assert.equal(refName(req.properties?.device), 'DeviceInfo', `${id}: поле device`);
    assert.ok(!(req.required ?? []).includes('device'), `${id}: device необязателен в схеме`);
    assert.match(op.description, /identity\.device_required/, `${id}: правило в description`);
    assert.ok(op['x-error-codes'].includes('identity.device_required'), `${id}: x-error-codes`);
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

// R6: x-idempotency на public — ровно у трёх операций с секретами и только no-store (другое значение middleware не
// узнаёт и молча сохранил бы тело).
test('x-idempotency: no-store — ровно у трёх операций с секретами (setupTotp, confirmTotp, regenerateRecoveryCodes)', () => {
  const marked = [...ops.values()].filter(({ op }) => op['x-idempotency'] !== undefined);
  for (const { op } of marked) assert.equal(op['x-idempotency'], 'no-store', op.operationId);
  assert.deepEqual(marked.map(({ op }) => op.operationId).sort(), [
    'confirmTotp',
    'regenerateRecoveryCodes',
    'setupTotp',
  ]);
  for (const { op } of marked) {
    assert.match(op.description, /409 idempotency\.replay_unavailable/, op.operationId);
  }
});

// §4: операция за флагом способа входа называет ключ (основной / аварийный) и 403 feature.disabled.
const FLAGS = {
  startMfaPasskey: ['auth_passkeys_login'],
  createSocialNonce: ['auth_apple', 'auth_apple_login', 'auth_google', 'auth_google_login'],
  socialLogin: ['auth_apple', 'auth_apple_login', 'auth_google', 'auth_google_login'],
  completeSocialSignup: ['auth_apple', 'auth_google'],
  startPasskeyLogin: ['auth_passkeys_login'],
  passkeyLogin: ['auth_passkeys_login'],
  reauthenticate: ['auth_passkeys_login', 'auth_apple_login', 'auth_google_login'],
  startReauthPasskey: ['auth_passkeys_login'],
  linkIdentity: ['auth_apple', 'auth_google'],
  startPasskeyRegistration: ['auth_passkeys'],
  registerPasskey: ['auth_passkeys'],
};

test('флаги в описаниях: 11 операций называют ключ флага и 403 feature.disabled', () => {
  assert.equal(Object.keys(FLAGS).length, 11);
  for (const [id, keys] of Object.entries(FLAGS)) {
    const d = ops.get(id).op.description;
    assert.match(d, /403 feature\.disabled/, id);
    for (const k of keys) assert.match(d, new RegExp(`\`${k}\``), `${id}: ${k}`);
  }
  // отвязка и удаление ключа — при любых флагах (§4.3)
  for (const id of ['unlinkIdentity', 'deletePasskey']) {
    assert.match(ops.get(id).op.description, /при любых флагах/, id);
  }
  const m = ops.get('getAuthMethods').op.description;
  assert.match(m, /Google/);
  assert.match(m, /Apple/);
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
  socialLogin: [['403', 'identity.account_blocked', 'blocked_until']],
  passkeyLogin: [['403', 'identity.account_blocked', 'blocked_until']],
  reauthenticate: [['400', 'identity.reauth_invalid', 'attempts_left']],
  submitCaptainApplication: [
    ['409', 'identity.captain_ineligible', 'reasons'],
    ['409', 'identity.application_cooldown', 'available_at'],
  ],
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
  for (const id of [
    'changePassword',
    'startEmailChange',
    'deleteAccount',
    'linkIdentity',
    'unlinkIdentity',
    'setupTotp',
    'disableTotp',
    'regenerateRecoveryCodes',
    'startPasskeyRegistration',
    'deletePasskey',
  ]) {
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

// Реестр схем Task 3 (Приложение 2) + PasskeyLoginRequest (тело passkeyLogin с device: в реестре не названо, а
// PasskeyAssertion закрыт additionalProperties: false — отступление в отчёте).
const SCHEMAS_T3 = [
  'AuthMethods',
  'AuthMethodSwitch',
  'PasskeyMethodSwitch',
  'SocialProvider',
  'SocialNonceRequest',
  'SocialNonce',
  'SocialLoginRequest',
  'AppleAuthorization',
  'SocialSignupCompleteRequest',
  'SocialLinkRequest',
  'MfaPasskeyOptionsRequest',
  'ReauthRequest',
  'ReauthSocial',
  'ReauthResult',
  'MyAuthMethods',
  'LinkedIdentity',
  'Passkey',
  'MfaStatus',
  'TotpSetup',
  'TotpConfirmRequest',
  'RecoveryCodes',
  'PasskeyRegisterRequest',
  'PasskeyRenameRequest',
  'PasskeyLoginRequest',
];

test('реестр схем Task 3 — все схемы на месте', () => {
  assert.deepEqual(
    SCHEMAS_T3.filter((n) => !schema(n)),
    [],
  );
});

test('Task 3: SocialProvider — apple | google, открытый; ReauthRequest — одна схема «ровно одно из» (R11)', () => {
  assert.deepEqual(schema('SocialProvider').enum, ['apple', 'google']);
  assert.equal(schema('SocialProvider')['x-extensible-enum'], true);
  const r = schema('ReauthRequest');
  assert.equal(r.oneOf, undefined);
  assert.equal(r.required, undefined);
  assert.deepEqual(Object.keys(r.properties).sort(), [
    'passkey',
    'password',
    'recovery_code',
    'social',
    'totp_code',
  ]);
  assert.match(r.description, /ровно одно/);
  assert.match(ops.get('reauthenticate').op.description, /identity\.reauth_method_not_allowed/);
});

test('Task 3: WebAuthn — options и credential как есть (additionalProperties), пример W3C L3', () => {
  const o = schema('WebAuthnOptions').properties.options;
  assert.equal(o.additionalProperties, true);
  assert.ok(o.example.challenge && o.example.rpId);
  assert.equal(schema('WebAuthnCredential').additionalProperties, true);
  for (const id of ['startPasskeyLogin']) {
    const op = ops.get(id).op;
    assert.equal(op.requestBody, undefined, id);
    assert.match(op.description, /без тела/, id);
  }
});

test('Task 3: RecoveryCodes — ровно 10 кодов; MfaStatus.reset_scheduled_at обнуляемое', () => {
  const rc = schema('RecoveryCodes').properties.recovery_codes;
  assert.equal(rc.minItems, 10);
  assert.equal(rc.maxItems, 10);
  assert.equal(schema('MfaStatus').properties.reset_scheduled_at.nullable, true);
});

test('Task 3: socialLogin — ветки §3.5 в описании; Apple authorization_code — зачем', () => {
  const d = ops.get('socialLogin').op.description;
  for (const w of ['signup_required', 'mfa_required', 'deletion_pending', 'автопривязк']) {
    assert.ok(d.includes(w), w);
  }
  assert.match(schema('AppleAuthorization').properties.authorization_code.description, /отзыв/);
  assert.match(ops.get('cancelMyMfaReset').op.description, /204/);
});

// Реестр схем Task 4 (Приложение 2): капитанство.
test('Task 4: схемы капитанства и открытые перечисления §3.12, §6.2.4, §7.3', () => {
  for (const n of [
    'Captaincy',
    'CaptainPermit',
    'CaptainEligibility',
    'CaptainIneligibility',
    'CaptainApplicationStatus',
    'CaptainApplication',
    'CaptainApplicationPage',
    'CaptainApplicationSubmitRequest',
    'CaptainRejectionCode',
  ]) {
    assert.ok(schema(n), n);
  }
  const enums = {
    CaptainIneligibility: [
      'too_young',
      'restricted',
      'profile_incomplete',
      'captain_elsewhere_in_city',
      'already_permitted',
      'checks_unavailable',
    ],
    CaptainApplicationStatus: ['pending', 'approved', 'rejected', 'withdrawn'],
    CaptainRejectionCode: [
      'profile_incomplete',
      'insufficient_info',
      'contact_unreachable',
      'suspicious_activity',
      'duplicate_account',
      'other',
    ],
  };
  for (const [name, values] of Object.entries(enums)) {
    assert.deepEqual(schema(name).enum, values, name);
    assert.equal(schema(name)['x-extensible-enum'], true, name);
  }
});

test('Task 4: заявка — пределы §5.2 (note ≤ 1000, прочие ≤ 200, contact_link https ≤ 300, phone E.164)', () => {
  const p = schema('CaptainApplicationSubmitRequest').properties;
  assert.equal(schema('CaptainApplicationSubmitRequest').required, undefined);
  assert.equal(p.note.maxLength, 1000);
  for (const f of ['proposed_format', 'proposed_pitch', 'play_frequency']) {
    assert.equal(p[f].maxLength, 200, f);
  }
  assert.equal(p.contact_link.maxLength, 300);
  assert.match(p.contact_link.pattern, /\^https:/);
  assert.ok(new RegExp(p.phone.pattern).test('+79181234567'));
  assert.ok(!new RegExp(p.phone.pattern).test('89181234567'));
  const d = ops.get('submitCaptainApplication').op.description;
  assert.match(d, /checks_unavailable/);
  assert.match(d, /текущий город/);
  assert.match(d, /не подтверждается/);
});

test('Task 4: страница заявок — cursor и limit (platform/page v1), компоненты корня', () => {
  const names = ops
    .get('listMyCaptainApplications')
    .op.parameters.map((p) => p.$ref?.split('/').at(-1))
    .sort();
  assert.deepEqual(names, ['Cursor', 'Limit']);
  const page = schema('CaptainApplicationPage');
  assert.deepEqual(page.required, ['items', 'next_cursor']);
  assert.equal(page.properties.next_cursor.nullable, true);
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

// Minor ревью группы А (m1): операция гасит собственную сессию — повтор после обрыва проходит auth.Middleware раньше
// idempotency.Middleware и получает 401, а не сохранённый ответ; описание говорит это честно.
test('m1: logout, revokeMySession, deleteAccount — повтор после обрыва может получить 401 auth.unauthenticated', () => {
  for (const id of ['logout', 'revokeMySession', 'deleteAccount']) {
    const d = ops.get(id).op.description;
    assert.match(d, /auth\.unauthenticated/, id);
    assert.match(d, /refreshSession/, `${id}: refreshSession не вызывать`);
    assert.doesNotMatch(d, /тот же ответ/, `${id}: не обещает сохранённый ответ`);
  }
});

// m2: предел длины ника в схеме отвечает 400 validation.failed раньше сервера — описания называют предел.
test('m2: ник длиннее maxLength — 400 validation.failed, сказано в описаниях', () => {
  const nick = ops.get('checkNickname').op;
  const max = nick.parameters.find((p) => p.name === 'nickname').schema.maxLength;
  assert.match(nick.description, new RegExp(`длиннее\\s+${max}`, 'i'));
  assert.match(nick.description, /400 validation\.failed/);
  let n = 0;
  for (const [name, s] of Object.entries(doc.components.schemas)) {
    for (const [field, p] of Object.entries(s.properties ?? {})) {
      if (!/nickname/.test(field) || p.maxLength === undefined) continue;
      n++;
      assert.match(p.description, new RegExp(`длиннее\\s+${p.maxLength}`, 'i'), `${name}.${field}`);
      assert.match(p.description, /400 validation\.failed/, `${name}.${field}`);
    }
  }
  assert.ok(n >= 4, `полей ника ${n} — страж вхолостую`);
});

// m3: явный 4xx-ответ — не «ответ одного кода»: туда же приходят общие коды платформы, разбирать по code.
test('m3: явные 4xx операций identity — «разбирайте по code» и общие коды платформы', () => {
  let n = 0;
  for (const { op } of ops.values()) {
    if (!op.tags?.includes('identity')) continue;
    for (const [status, raw] of Object.entries(op.responses ?? {})) {
      if (!/^4/.test(status)) continue;
      n++;
      const d = deref('responses', raw).description;
      assert.match(d, /разбирайте по `code`/i, `${op.operationId} ${status}`);
      assert.match(d, /общие коды платформы/, `${op.operationId} ${status}`);
    }
  }
  assert.ok(n >= 10, `явных 4xx ${n} — страж вхолостую`);
});

// m5: неверная почта (§5.7) и дата рождения (§3.1 п. 2.1) — своих кодов нет, это 400 validation.failed с полем.
test('m5: неверная почта и дата рождения — 400 validation.failed по полю, сказано в описаниях', () => {
  const rows = {
    startSignup: ['body.email', 'body.birth_date'],
    login: ['body.email'],
    startPasswordReset: ['body.email'],
    startEmailChange: ['body.new_email'],
  };
  for (const [id, fields] of Object.entries(rows)) {
    const d = ops.get(id).op.description;
    assert.match(d, /400 validation\.failed/, id);
    for (const f of fields) assert.ok(d.includes(`\`${f}\``), `${id}: поле ${f}`);
  }
});

// m6: операции без тела — описание говорит «без тела» (спека пишет `{}`; requestBody в контракте нет).
test('m6: createDeviceChallenge — без тела', () => {
  for (const id of ['createDeviceChallenge']) {
    const op = ops.get(id).op;
    assert.equal(op.requestBody, undefined, id);
    assert.match(op.description, /без тела/, id);
  }
});

// app_version: iOS CFBundleShortVersionString бывает «1.4» — принимаются две или три части.
test('DeviceInfo.app_version: 1.4 и 1.4.0 проходят, 1.4.0.1, v1.4, 01.4 — нет', () => {
  const p = schema('DeviceInfo').properties.app_version;
  const re = new RegExp(p.pattern);
  for (const ok of ['1.4', '1.4.0', '0.1', '12.0.3']) assert.ok(re.test(ok), ok);
  for (const bad of ['1', '1.4.0.1', 'v1.4', '01.4', '1.4-beta', '']) assert.ok(!re.test(bad), bad);
  assert.match(p.description, /1\.4\.0/);
});

test('info.description — раздел «Вход и токены»', () => {
  const d = doc.info.description;
  assert.match(d, /Вход и токены/);
  assert.match(d, /refreshSession/);
  assert.match(d, /Keychain/);
  assert.match(d, /identity\.restricted/);
});

// Решение контроллера (ревью Б): аттестация §3.8 — «на регистрации и входе», в том числе ключом и дорегистрацией;
// код — только в строгом режиме (по умолчанию observe — 403 humancheck.required).
test('device_untrusted — у всех операций регистрации и входа, в описании «строгий режим»', () => {
  for (const id of [
    'startSignup',
    'login',
    'socialLogin',
    'completeSocialSignup',
    'passkeyLogin',
  ]) {
    const { op } = ops.get(id);
    assert.ok(op['x-error-codes'].includes('identity.device_untrusted'), id);
    assert.match(op.description, /`identity\.device_untrusted` \(403\)[^\n]*строгий режим/, id);
  }
});

// Ревью Б m1: §3.5 не обещает, что signup_token переживает отказ по нику.
test('completeSocialSignup — не обещает «токен не тратится» сверх спеки', () => {
  assert.doesNotMatch(ops.get('completeSocialSignup').op.description, /не тратится/);
});
