import { screen } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { meOwner } from '../test/me';
import { renderWithIntl, renderWithProviders } from '../test/render';
import { useDateTime } from './format';

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
