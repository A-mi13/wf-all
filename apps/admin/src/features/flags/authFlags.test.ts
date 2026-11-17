import { expect, expectTypeOf, test } from 'vitest';
import type { components } from '../../api/gen/admin';
import { emergencyMethod, LOGIN_FLAG_METHOD } from './authFlags';

type FeatureFlagKey = components['schemas']['FeatureFlagKey'];
type Method = components['schemas']['IdentityAuthMethodKind'];

// Аварийные флаги входа (план I-4, R3): связь «флаг → способ статистики» — знание экрана, ключи — только флаги
// контракта (FeatureFlagKey), способы — только IdentityAuthMethodKind.
test('ключи ⊆ FeatureFlagKey, значения ⊆ IdentityAuthMethodKind', () => {
  expectTypeOf<keyof typeof LOGIN_FLAG_METHOD>().toExtend<FeatureFlagKey>();
  expectTypeOf<(typeof LOGIN_FLAG_METHOD)[keyof typeof LOGIN_FLAG_METHOD]>().toExtend<Method>();
  expect(LOGIN_FLAG_METHOD).toEqual({
    auth_apple_login: 'apple',
    auth_google_login: 'google',
    auth_passkeys_login: 'passkeys',
  });
});

test('emergencyMethod: аварийный флаг — способ, прочие и незнакомые — undefined', () => {
  expect(emergencyMethod('auth_google_login')).toBe('google');
  expect(emergencyMethod('auth_google')).toBeUndefined();
  expect(emergencyMethod('toString')).toBeUndefined();
  expect(emergencyMethod('future_flag')).toBeUndefined();
});
