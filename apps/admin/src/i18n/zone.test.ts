import { expect, test } from 'vitest';
import { dayStartUtc as fromAudit } from '../features/audit/search';
import { dayStartUtc } from './zone';

// Местная полночь в поясе (план UF2a.9, m8): переехала из features/audit/search.ts в src/i18n (прямой Intl в
// экранах запрещён lint-правилом) без изменения поведения; журнал берёт её отсюда.

test('местная полночь в двух поясах и по переходам времени', () => {
  expect(dayStartUtc('2026-10-13', 'Europe/Moscow')).toBe('2026-10-12T21:00:00.000Z');
  expect(dayStartUtc('2026-10-13', 'America/New_York')).toBe('2026-10-13T04:00:00.000Z');
  expect(dayStartUtc('2026-11-02', 'America/New_York')).toBe('2026-11-02T05:00:00.000Z');
  expect(dayStartUtc('2026-10-04', 'Australia/Sydney')).toBe('2026-10-03T14:00:00.000Z');
});

test('полуночи нет (перевод вперёд в 00:00) — первый момент дня; shiftDays — следующий день', () => {
  expect(dayStartUtc('2026-09-06', 'America/Santiago')).toBe('2026-09-06T04:00:00.000Z');
  expect(dayStartUtc('2026-09-05', 'America/Santiago', 1)).toBe('2026-09-06T04:00:00.000Z');
  expect(dayStartUtc('2026-03-08', 'America/Havana')).toBe('2026-03-08T05:00:00.000Z');
});

test('журнал использует ту же функцию', () => {
  expect(fromAudit).toBe(dayStartUtc);
});
