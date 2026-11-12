import { expect, test } from 'vitest';
import { meModerator, meOwner } from '../test/me';
import { resolveRoute, safeReturn } from './guard';

test('возврат — путь приложения с параметрами', () => {
  expect(safeReturn('/staff?tab=sessions')).toBe('/staff?tab=sessions');
});

test('чужие адреса, экраны входа и не строки отвергаются', () => {
  for (const v of [
    '//evil.example/x',
    '/\\evil.example',
    'https://evil.example',
    'staff',
    '/',
    '/login?return=/staff',
    '/accept',
    '/setup-2fa',
    undefined,
    42,
  ]) {
    expect(safeReturn(v), String(v)).toBeUndefined();
  }
});

// Браузер выбрасывает из URL табуляцию и переводы строк: «/\t/evil.example» превращается в
// «//evil.example» — протокол-относительный адрес чужого сайта.
test('управляющие символы отвергаются', () => {
  for (const v of ['/\t/evil.example', '/\n/evil.example', '/\r/evil.example', '/staff\u0000']) {
    expect(safeReturn(v), JSON.stringify(v)).toBeUndefined();
  }
});

test('без сессии — вход с возвратом; после явного выхода — без возврата', () => {
  const loc = { pathname: '/staff', href: '/staff?tab=sessions', search: {} };
  expect(resolveRoute({ status: 'anonymous' }, loc)).toEqual({
    to: '/login',
    search: { return: '/staff?tab=sessions' },
  });
  expect(resolveRoute({ status: 'anonymous', signedOut: true }, loc)).toEqual({ to: '/login' });
});

// Стартовая после входа — главная для всех (спека UI-фундамента §4.13, план UF4.1); адрес из ?return= (письмо,
// истёкшая сессия) важнее главной.
test('вход без return — главная; с return — указанный экран', () => {
  for (const me of [meOwner, meModerator]) {
    const active = { status: 'active' as const, me, reauthRequired: false };
    expect(resolveRoute(active, { pathname: '/login', href: '/login', search: {} })).toEqual({
      to: '/',
    });
    expect(
      resolveRoute(active, {
        pathname: '/login',
        href: '/login?return=%2Fstaff',
        search: { return: '/staff' },
      }),
    ).toEqual({ href: '/staff' });
  }
});

test('активный сотрудник на корне — главная открыта, без редиректа', () => {
  const active = { status: 'active' as const, me: meOwner, reauthRequired: false };
  expect(resolveRoute(active, { pathname: '/', href: '/', search: {} })).toBeNull();
});
