import { afterEach, describe, expect, test, vi } from 'vitest';
import { firstDayOfWeek } from './week';

// Первый день недели календаря — по языку интерфейса (спека UI-фундамента §4.17), не по стране и не по ОС.
describe('firstDayOfWeek', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  test('ru — понедельник, en — воскресенье (CLDR)', () => {
    expect(firstDayOfWeek('ru')).toBe(1);
    expect(firstDayOfWeek('en')).toBe(7);
  });

  test('нет getWeekInfo — свойство weekInfo (часть движков)', () => {
    const proto = Intl.Locale.prototype as unknown as Record<string, unknown>;
    const had = Object.getOwnPropertyDescriptor(proto, 'getWeekInfo');
    Object.defineProperty(proto, 'getWeekInfo', { configurable: true, value: undefined });
    Object.defineProperty(proto, 'weekInfo', {
      configurable: true,
      get: () => ({ firstDay: 7 }),
    });
    try {
      expect(firstDayOfWeek('ru')).toBe(7);
    } finally {
      if (had) Object.defineProperty(proto, 'getWeekInfo', had);
      Reflect.deleteProperty(proto, 'weekInfo');
    }
  });

  test('нет API вовсе — запасная таблица: ru → 1, en → 7, незнакомый — 1', () => {
    const proto = Intl.Locale.prototype as unknown as Record<string, unknown>;
    const had = Object.getOwnPropertyDescriptor(proto, 'getWeekInfo');
    const hadInfo = Object.getOwnPropertyDescriptor(proto, 'weekInfo');
    Object.defineProperty(proto, 'getWeekInfo', { configurable: true, value: undefined });
    Object.defineProperty(proto, 'weekInfo', { configurable: true, value: undefined });
    try {
      expect(firstDayOfWeek('ru')).toBe(1);
      expect(firstDayOfWeek('en')).toBe(7);
      expect(firstDayOfWeek('en-GB')).toBe(7);
      expect(firstDayOfWeek('de')).toBe(1);
    } finally {
      if (had) Object.defineProperty(proto, 'getWeekInfo', had);
      if (hadInfo) Object.defineProperty(proto, 'weekInfo', hadInfo);
      else Reflect.deleteProperty(proto, 'weekInfo');
    }
  });
});
