import { screen } from '@testing-library/react';
import { afterEach, expect, test, vi } from 'vitest';
import { meOwner } from '../test/me';
import { renderWithProviders } from '../test/render';
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
