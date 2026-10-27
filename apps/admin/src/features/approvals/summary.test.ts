import { expect, test } from 'vitest';
import { summarizePayload } from './summary';

// payload запроса — поля действия staff.* (backend/internal/staff/internal/app/actions.go): экран
// берёт только знакомые поля нужного типа; служебные (v, init_id, init_ver, version) и незнакомые —
// мимо, мусор не падает.

test('staff.invite: имя, почта как есть (xn-- не декодируется), роль, города', () => {
  expect(
    summarizePayload({
      v: 1,
      init_id: '0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c6d',
      init_ver: 4,
      email: 'ivan@xn--d1acufc.xn--p1ai',
      display_name: 'Иван П.',
      role: 'moderator',
      city_ids: ['c1', 'c2'],
    }),
  ).toEqual({
    name: 'Иван П.',
    email: 'ivan@xn--d1acufc.xn--p1ai',
    role: 'moderator',
    cityIds: ['c1', 'c2'],
  });
});

test('staff.role_change: было → стало', () => {
  expect(
    summarizePayload({
      v: 1,
      target_id: 't1',
      version: 3,
      from_role: 'moderator',
      from_city_ids: ['c1'],
      role: 'support',
      city_ids: [],
    }),
  ).toEqual({ fromRole: 'moderator', fromCityIds: ['c1'], role: 'support', cityIds: [] });
});

test('staff.access_reset / revoke_owner: роль, причина, комментарий', () => {
  expect(
    summarizePayload({
      v: 1,
      target_id: 't1',
      version: 1,
      role: 'owner',
      reason: 'left_team',
      comment: 'ушёл в другой проект',
    }),
  ).toEqual({ role: 'owner', reason: 'left_team', comment: 'ушёл в другой проект' });
});

test('мусор: не строки и не массивы строк, пустые строки — мимо', () => {
  expect(
    summarizePayload({
      email: 42,
      display_name: '',
      role: { x: 1 },
      city_ids: ['c1', 7],
      reason: null,
      comment: '   ',
      unknown: 'x',
    }),
  ).toEqual({});
});
