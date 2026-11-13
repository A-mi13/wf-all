import { screen } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { meOwner } from '../test/me';
import { renderWithIntl, renderWithProviders } from '../test/render';
import { useDateTime, useMoney, useNumber } from './format';

// Ревью Task 14 (дефект рядом): один способ форматировать даты — useFormatter use-intl с часовым
// поясом из LocaleProvider (пояс браузера). Без timeZone use-intl сообщает ENVIRONMENT_FALLBACK
// в консоль на каждом вызове.

afterEach(() => vi.restoreAllMocks());

function When({ iso }: { iso: string }) {
  const dateTime = useDateTime();
  return <p>{dateTime(iso)}</p>;
}

test('дата и время — на языке интерфейса, в поясе браузера, без ошибок use-intl', () => {
  const errors = vi.spyOn(console, 'error').mockImplementation(() => {});
  renderWithProviders(<When iso="2026-10-13T09:15:00Z" />, { me: { ...meOwner, locale: 'en' } });
  const want = new Intl.DateTimeFormat('en', {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
  }).format(new Date('2026-10-13T09:15:00Z'));
  expect(screen.getByText(want)).toBeInTheDocument();
  expect(errors).not.toHaveBeenCalled();
});

// Уборка №6, R4: компоненты src/ui в тестах (renderWithIntl) форматируют даты так же, как в
// приложении — с явным поясом, а не поясом машины по умолчанию и с ENVIRONMENT_FALLBACK в консоли.
test('renderWithIntl: пояс задан явно (по умолчанию — браузера), без ошибок use-intl', () => {
  const errors = vi.spyOn(console, 'error').mockImplementation(() => {});
  const iso = '2026-10-13T22:15:00Z';
  const fmt = (timeZone: string) =>
    new Intl.DateTimeFormat('ru', { dateStyle: 'medium', timeStyle: 'short', timeZone }).format(
      new Date(iso),
    );
  const tokyo = renderWithIntl(<When iso={iso} />, { timeZone: 'Asia/Tokyo' });
  expect(screen.getByText(fmt('Asia/Tokyo'))).toBeInTheDocument();
  tokyo.unmount();
  const ny = renderWithIntl(<When iso={iso} />, { timeZone: 'America/New_York' });
  expect(screen.getByText(fmt('America/New_York'))).toBeInTheDocument();
  ny.unmount();
  renderWithIntl(<When iso={iso} />);
  expect(
    screen.getByText(fmt(Intl.DateTimeFormat().resolvedOptions().timeZone)),
  ).toBeInTheDocument();
  expect(errors).not.toHaveBeenCalled();
});

// Деньги (спека UI-фундамента §4.7, U13, правило 2 CLAUDE.md): минорные единицы целым (строкой или безопасным
// числом) + ISO 4217 + экспонента параметром → десятичная строка без деления и без float, в Intl — строкой.
function Money({
  minor,
  currency,
  exponent,
}: {
  minor: string | number;
  currency: string;
  exponent: number;
}) {
  const money = useMoney();
  return <p data-testid="money">{money(minor, currency, exponent)}</p>;
}

const money = (
  minor: string | number,
  currency: string,
  exponent: number,
  locale: 'ru' | 'en' = 'ru',
) => {
  const { unmount } = renderWithIntl(
    <Money minor={minor} currency={currency} exponent={exponent} />,
    {
      locale,
    },
  );
  const text = screen.getByTestId('money').textContent ?? '';
  unmount();
  return text.replace(/\s/g, ' ');
};

describe('useMoney', () => {
  test('больше 2^53 — точная строка (без float)', () => {
    expect(money('9007199254740993', 'RUB', 2)).toBe('90 071 992 547 409,93 ₽');
    expect(money('9007199254740993', 'USD', 2, 'en')).toBe('$90,071,992,547,409.93');
  });

  test('отрицательные и меньше одной единицы', () => {
    expect(money('-500', 'RUB', 2)).toBe('-5,00 ₽');
    expect(money('-5', 'RUB', 2)).toBe('-0,05 ₽');
    expect(money(7, 'RUB', 2)).toBe('0,07 ₽');
  });

  test('экспонента 0 (JPY) и 3 (KWD) — из параметра, а не CLDR', () => {
    expect(money('1234', 'JPY', 0)).toBe('1 234 ¥');
    expect(money('1234567', 'KWD', 3)).toBe('1 234,567 KWD');
    expect(money(123456, 'RUB', 2)).toBe('1 234,56 ₽');
  });

  // Ревью I3: у JPY, KWD, RUB экспонента совпадает с CLDR — без опций знаков вышло бы то же. Здесь — расходится:
  // IQD по ISO 4217 — 3 знака, CLDR — 0 (без опций «1 235 IQD», сумма округлена); RUB с 0 — CLDR дал бы «,00».
  test('экспонента расходится с CLDR — побеждает параметр', () => {
    expect(money('1234567', 'IQD', 3)).toBe('1 234,567 IQD');
    expect(money('1234', 'RUB', 0)).toBe('1 234 ₽');
  });

  test('ноль со знаком минус — без минуса', () => {
    expect(money('-0', 'RUB', 2)).toBe('0,00 ₽');
    expect(money('-000', 'RUB', 2)).toBe('0,00 ₽');
  });

  test('не целое — ошибка, а не округление', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    expect(() => money('12.5', 'RUB', 2)).toThrow(RangeError);
    expect(() => money(1.5, 'RUB', 2)).toThrow(RangeError);
    expect(() => money(2 ** 53, 'RUB', 2)).toThrow(RangeError);
    expect(() => money('100', 'RUB', -1)).toThrow(RangeError);
  });
});

function Num({ n }: { n: number }) {
  const number = useNumber();
  return <p data-testid="num">{number(n)}</p>;
}

test('useNumber — на языке интерфейса', () => {
  renderWithIntl(<Num n={1234567} />, { locale: 'en' });
  expect(screen.getByTestId('num')).toHaveTextContent('1,234,567');
});
