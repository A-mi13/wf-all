import { useCallback } from 'react';
import { type NumberFormatOptions, useFormatter, useLocale } from 'use-intl';

// Дата и время на языке интерфейса в поясе браузера сотрудника («13 окт. 2026 г., 12:15») —
// единственный способ форматировать даты в админке (журнал, сеансы, сотрудники). Пояс задаёт
// LocaleProvider (IntlProvider timeZone): без него use-intl пишет ENVIRONMENT_FALLBACK в консоль.
export function useDateTime(): (iso: string) => string {
  const format = useFormatter();
  return useCallback(
    (iso: string) => format.dateTime(new Date(iso), { dateStyle: 'medium', timeStyle: 'short' }),
    [format],
  );
}

// Пояс браузера сотрудника — для LocaleProvider и тестового renderWithIntl (один источник: в тестах
// даты идут тем же путём, что в приложении).
export function browserTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone;
}

// Число на языке интерфейса (разряды, дробь) — единственный способ в экранах (прямой Intl в src/features
// запрещён lint-правилом, спека UI-фундамента §4.7).
export function useNumber(): (n: number, opts?: NumberFormatOptions) => string {
  const format = useFormatter();
  return useCallback((n: number, opts?: NumberFormatOptions) => format.number(n, opts), [format]);
}

const INTEGER = /^-?\d+$/;

// Минорные единицы → десятичная строка без деления и без float (правило 2 CLAUDE.md): "123456", 2 → "1234.56",
// "-5", 2 → "-0.05". minor — целое строкой (int64 из API) или безопасное целое; иное — RangeError, а не
// молчаливое округление.
export function minorToDecimal(minor: string | number, exponent: number): string {
  if (!Number.isInteger(exponent) || exponent < 0 || exponent > 20)
    throw new RangeError(`экспонента валюты: ${exponent}`);
  const raw =
    typeof minor === 'number' ? (Number.isSafeInteger(minor) ? String(minor) : '') : minor;
  if (!INTEGER.test(raw))
    throw new RangeError(`сумма в минорных единицах не целое: ${String(minor)}`);
  // «-0» — ноль без знака (иначе «-0,00 ₽»)
  const negative = raw.startsWith('-') && /[1-9]/.test(raw);
  const digits = raw.replace('-', '').padStart(exponent + 1, '0');
  const whole = digits.slice(0, digits.length - exponent);
  const fraction = digits.slice(digits.length - exponent);
  return `${negative ? '-' : ''}${whole}${exponent > 0 ? `.${fraction}` : ''}`;
}

// Деньги (спека §4.7, план U13): минорные единицы + ISO 4217 + число знаков валюты. Справочника экспонент в
// админке пока нет (появится с первым экраном сумм — тот же, что у бэкенда, не CLDR): вызов передаёт её явно.
// В Intl.NumberFormat#format уходит десятичная СТРОКА — она форматируется точно и больше 2^53.
export function useMoney(): (minor: string | number, currency: string, exponent: number) => string {
  const locale = useLocale();
  return useCallback(
    (minor: string | number, currency: string, exponent: number) => {
      const decimal = minorToDecimal(minor, exponent);
      return new Intl.NumberFormat(locale, {
        style: 'currency',
        currency,
        minimumFractionDigits: exponent,
        maximumFractionDigits: exponent,
      }).format(decimal as Intl.StringNumericLiteral);
    },
    [locale],
  );
}

export type RelativeMode = 'auto' | 'relative' | 'exact';

const SECOND = 1000;
const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 3600 * SECOND],
  ['month', 30 * 24 * 3600 * SECOND],
  ['day', 24 * 3600 * SECOND],
  ['hour', 3600 * SECOND],
  ['minute', 60 * SECOND],
  ['second', SECOND],
];
const WEEK = 7 * 24 * 3600 * SECOND;

// Текст времени относительно now (спека §4.7): прошлое < 1 мин — «только что» (justNow), auto: < 7 дней —
// относительно, старше — дата; relative — всегда относительно. Единица — крупнейшая целая, значение — вниз до
// целого (6 дн 23 ч — «6 дней назад», а не округлённые «7»). exact — точное время.
export function useRelativeTime(): (
  value: Date,
  now: Date,
  mode: RelativeMode,
  justNow: string,
) => string {
  const locale = useLocale();
  const format = useFormatter();
  return useCallback(
    (value: Date, now: Date, mode: RelativeMode, justNow: string) => {
      if (mode === 'exact') return exactTime(format, value);
      const diff = value.getTime() - now.getTime();
      const abs = Math.abs(diff);
      // меньше минуты в любую сторону: снимок useNow('minute') отстаёт до минуты, часы клиента и сервера
      // расходятся — свежая запись не бывает «через 29 секунд» (ревью UF2a/II-B, I1)
      if (abs < 60 * SECOND) return justNow;
      if (mode === 'auto' && abs >= WEEK) return format.dateTime(value, { dateStyle: 'medium' });
      const [unit, size] = UNITS.find(([, ms]) => abs >= ms) ?? ['second', SECOND];
      const n = Math.floor(abs / size);
      return new Intl.RelativeTimeFormat(locale, { numeric: 'always' }).format(
        diff < 0 ? -n : n,
        unit,
      );
    },
    [locale, format],
  );
}

// Точное время с секундами и поясом интерфейса («28 окт. 2026 г., 14:05:12 GMT+3»).
export function useExactTime(): (value: Date) => string {
  const format = useFormatter();
  return useCallback((value: Date) => exactTime(format, value), [format]);
}

function exactTime(format: ReturnType<typeof useFormatter>, value: Date): string {
  return format.dateTime(value, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    timeZoneName: 'short',
  });
}
