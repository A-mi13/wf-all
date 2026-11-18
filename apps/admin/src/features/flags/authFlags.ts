import type { components } from '../../api/gen/admin';

type FeatureFlagKey = components['schemas']['FeatureFlagKey'];
type Method = components['schemas']['IdentityAuthMethodKind'];

// Аварийные флаги входа (спека identity §4.3, §7.5; план I-4, R3): выключение отключает вход этим способом всем —
// перед ним экран показывает, сколько игроков входят только так (getAuthMethodsUsage, only_method). Связь «флаг →
// способ» — знание экрана, не правило флага (правила — FeatureFlag.rules сервера); страж — authFlags.test.ts.
export const LOGIN_FLAG_METHOD = {
  auth_apple_login: 'apple',
  auth_google_login: 'google',
  auth_passkeys_login: 'passkeys',
} as const satisfies Partial<Record<FeatureFlagKey, Method>>;

const METHODS: ReadonlyMap<string, Method> = new Map(Object.entries(LOGIN_FLAG_METHOD));

// Способ аварийного флага или undefined (ключ — любой: флаг новее админки приходит своим ключом).
export const emergencyMethod = (key: string): Method | undefined => METHODS.get(key);
