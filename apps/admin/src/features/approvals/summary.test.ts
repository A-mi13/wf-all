import { renderHook } from '@testing-library/react';
import { describe, expect, test } from 'vitest';
import { meOwner } from '../../test/me';
import { providers } from '../../test/render';
import { useCountrySummary } from './labels';
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

// План B, Task 17: действия geo (payload — Task 13): правила страны — код и «было → стало» по
// полям from/to (валюта, пороги, включение), новая страна — все её правила. Только знакомые поля
// нужного типа; незнакомое поле и неверный тип — мимо.
describe('geo.country_rules', () => {
  const payload = (from: unknown, to: unknown) => ({
    v: 1,
    init_id: '0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c6d',
    init_ver: 4,
    country_id: '0192f1c4-0000-7b21-9c5d-0000000000c1',
    code: 'RU',
    version: 7,
    from,
    to,
    patch: to,
  });

  test('валюта: RU: валюта RUB → KZT', () => {
    const s = summarizePayload(
      payload({ currency: 'RUB' }, { currency: 'KZT' }),
      'geo.country_rules',
    );
    expect(s).toEqual({
      country: {
        code: 'RU',
        changes: [{ field: 'currency', from: 'RUB', to: 'KZT' }],
        others: [],
      },
    });
    expect(text(s)).toBe('RU: валюта RUB → KZT');
  });

  test('выключение: RU: включена → выключена', () => {
    const s = summarizePayload(
      payload({ is_enabled: true }, { is_enabled: false }),
      'geo.country_rules',
    );
    expect(text(s)).toBe('RU: включена → выключена');
  });

  // Ревью G8, I1: подтверждающий видит ВСЁ, что исполнится, — запрос несёт весь patch (поля «сразу»
  // тоже), а аудит запишет автором подтверждающего. Знакомые поля — «было → стало», незнакомые и
  // неверного типа — «и ещё N полей: <ключи>», а не молча мимо.
  test('валюта и телефонный код вместе — видны обе правки', () => {
    const s = summarizePayload(
      payload({ currency: 'RUB', phone_prefix: '+7' }, { currency: 'KZT', phone_prefix: '+8' }),
      'geo.country_rules',
    );
    expect(text(s)).toBe('RU: валюта RUB → KZT; телефонный код +7 → +8');
  });

  test('все поля patch по порядку: пороги, неделя, включение, названия', () => {
    const s = summarizePayload(
      payload(
        {
          age_of_majority: 18,
          min_signup_age: 14,
          week_starts_on: 1,
          is_enabled: false,
          names: { en: 'Russia', ru: 'Россия' },
        },
        {
          age_of_majority: 21,
          min_signup_age: 16,
          week_starts_on: 7,
          is_enabled: true,
          names: { en: 'Russian Federation', ru: null },
        },
      ),
      'geo.country_rules',
    );
    expect(s.country?.changes).toEqual([
      { field: 'week_starts_on', from: 1, to: 7 },
      { field: 'min_signup_age', from: 14, to: 16 },
      { field: 'age_of_majority', from: 18, to: 21 },
      { field: 'is_enabled', from: false, to: true },
      { field: 'names', locale: 'en', from: 'Russia', to: 'Russian Federation' },
      { field: 'names', locale: 'ru', from: 'Россия', to: null },
    ]);
    expect(text(s)).toBe(
      'RU: начало недели пн → вс; возраст регистрации 14 → 16; совершеннолетие 18 → 21; ' +
        'выключена → включена; название (en) Russia → Russian Federation; название (ru) Россия → —',
    );
  });

  test('незнакомое поле и неверный тип — не молча: «и ещё N полей: ключи»', () => {
    const s = summarizePayload(
      payload(
        { currency: 5, min_signup_age: 14, timezone: 'Europe/Moscow' },
        { currency: 'KZT', min_signup_age: 16, timezone: 'Asia/Almaty', vat: 20 },
      ),
      'geo.country_rules',
    );
    expect(s.country?.others).toEqual(['currency', 'timezone', 'vat']);
    expect(text(s)).toBe('RU: возраст регистрации 14 → 16; и ещё 3 поля: currency, timezone, vat');
  });

  test('мусор: нет кода, from/to не объекты — ничего про страну', () => {
    expect(summarizePayload({ code: 7, from: {}, to: {} }, 'geo.country_rules')).toEqual({});
    expect(summarizePayload({ code: 'RU', from: 'x', to: null }, 'geo.country_rules')).toEqual({
      country: { code: 'RU', changes: [], others: [] },
    });
    expect(summarizePayload({ code: 'RU', from: [1], to: [2] }, 'geo.country_rules')).toEqual({
      country: { code: 'RU', changes: [], others: [] },
    });
  });

  test('поля geo у действия staff.* не читаются', () => {
    expect(
      summarizePayload(payload({ currency: 'RUB' }, { currency: 'KZT' }), 'staff.invite'),
    ).toEqual({});
  });
});

describe('geo.country_create', () => {
  const create = {
    v: 1,
    init_id: '0192f1c4-7a3e-7b21-9c5d-3e8f1a2b4c6d',
    init_ver: 4,
    code: 'KZ',
    currency: 'KZT',
    default_locale: 'ru',
    phone_prefix: '+7',
    week_starts_on: 1,
    min_signup_age: 14,
    age_of_majority: 18,
    is_enabled: false,
    names: { ru: 'Казахстан' },
  };

  test('новая страна KZ: KZT, ru, +7, неделя с пн, 14/18, выключена', () => {
    const s = summarizePayload(create, 'geo.country_create');
    expect(s).toEqual({
      newCountry: {
        code: 'KZ',
        currency: 'KZT',
        defaultLocale: 'ru',
        phonePrefix: '+7',
        weekStartsOn: 1,
        minSignupAge: 14,
        ageOfMajority: 18,
        isEnabled: false,
      },
    });
    expect(text(s)).toBe('новая страна KZ: KZT, ru, +7, неделя с пн, 14/18, выключена');
  });

  test('неверный тип и день недели вне 1–7 — части нет; незнакомое поле — мимо', () => {
    const s = summarizePayload(
      { ...create, currency: 42, week_starts_on: 8, is_enabled: true, extra: 'x' },
      'geo.country_create',
    );
    expect(text(s)).toBe('новая страна KZ: ru, +7, 14/18, включена');
  });

  test('без кода — ничего', () => {
    expect(summarizePayload({ ...create, code: '' }, 'geo.country_create')).toEqual({});
  });
});

test('en — перевод', () => {
  const s = summarizePayload(
    { code: 'RU', from: { is_enabled: true }, to: { is_enabled: false } },
    'geo.country_rules',
  );
  expect(text(s, 'en')).toBe('RU: enabled → disabled');
});

function text(s: ReturnType<typeof summarizePayload>, locale: 'ru' | 'en' = 'ru') {
  const me = { ...meOwner, locale };
  const { result } = renderHook(() => useCountrySummary(), { wrapper: providers({ me }) });
  return result.current(s);
}
