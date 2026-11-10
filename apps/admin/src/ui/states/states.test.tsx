import { act, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { ApiError } from '../../api/client';
import { renderWithIntl } from '../../test/render';
import {
  ConnectionErrorState,
  DesktopOnlyNotice,
  EmptyState,
  ErrorState,
  ForbiddenState,
  LoadingRows,
  OfflineBanner,
  PendingApprovalNotice,
  RateLimitedState,
} from './index';

afterEach(() => {
  vi.useRealTimers();
});

describe('RateLimitedState', () => {
  test('отсчёт по retryAfter, затем «Повторить»', () => {
    vi.useFakeTimers();
    const retry = vi.fn();
    renderWithIntl(<RateLimitedState retryAfter={3} onRetry={retry} />);
    expect(screen.getByRole('heading', { name: 'Слишком много запросов' })).toBeInTheDocument();
    const wait = screen.getByRole('button', { name: 'Подождать 3 с' });
    expect(wait).toBeDisabled();
    act(() => vi.advanceTimersByTime(1000));
    expect(screen.getByRole('button', { name: 'Подождать 2 с' })).toBeDisabled();
    act(() => vi.advanceTimersByTime(1999));
    expect(screen.getByRole('button', { name: 'Подождать 1 с' })).toBeDisabled();
    act(() => vi.advanceTimersByTime(1));
    const again = screen.getByRole('button', { name: 'Повторить' });
    expect(again).toBeEnabled();
    fireEvent.click(again);
    expect(retry).toHaveBeenCalledTimes(1);
  });

  test('новый retryAfter (повторный 429) запускает отсчёт заново', () => {
    vi.useFakeTimers();
    const { rerender } = renderWithIntl(<RateLimitedState retryAfter={2} onRetry={vi.fn()} />);
    act(() => vi.advanceTimersByTime(2000));
    expect(screen.getByRole('button', { name: 'Повторить' })).toBeEnabled();
    rerender(<RateLimitedState retryAfter={5} onRetry={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Подождать 5 с' })).toBeDisabled();
  });

  test('без retryAfter — повторить можно сразу', () => {
    renderWithIntl(<RateLimitedState onRetry={vi.fn()} />);
    expect(screen.getByRole('button', { name: 'Повторить' })).toBeEnabled();
  });
});

describe('OfflineBanner', () => {
  const setOnline = (v: boolean) => {
    vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(v);
    act(() => {
      window.dispatchEvent(new Event(v ? 'online' : 'offline'));
    });
  };
  afterEach(() => {
    vi.restoreAllMocks();
  });

  test('появляется по offline и исчезает по online; живая область постоянна', () => {
    renderWithIntl(<OfflineBanner />);
    const live = document.querySelector('[aria-live="polite"][data-wf-offline]');
    expect(live).toBeInTheDocument();
    expect(screen.queryByText(/Нет сети/)).not.toBeInTheDocument();
    setOnline(false);
    expect(live).toHaveTextContent(
      'Нет сети. Решения не отправятся, показываем последнее загруженное.',
    );
    setOnline(true);
    expect(screen.queryByText(/Нет сети/)).not.toBeInTheDocument();
    expect(live).toBeInTheDocument();
  });

  test('страница открылась без сети — баннер сразу', () => {
    vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(false);
    renderWithIntl(<OfflineBanner />);
    expect(screen.getByText(/Нет сети/)).toBeInTheDocument();
  });
});

describe('ErrorState', () => {
  test('текст по коду, код для поддержки и «Повторить»', () => {
    const retry = vi.fn();
    renderWithIntl(
      <ErrorState error={new ApiError({ status: 500, code: 'internal' })} onRetry={retry} />,
    );
    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent('Не удалось загрузить');
    expect(alert).toHaveTextContent('Что-то пошло не так. Попробуйте ещё раз');
    expect(alert).toHaveTextContent('Код ошибки: 500 · internal');
    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(retry).toHaveBeenCalledTimes(1);
  });

  test('retrying — «Повторить» занята, повторный клик не уходит', () => {
    const retry = vi.fn();
    renderWithIntl(<ErrorState error={new TypeError('x')} onRetry={retry} retrying />);
    const button = screen.getByRole('button', { name: 'Повторить' });
    expect(button).toHaveAttribute('aria-busy', 'true');
    expect(button).toHaveAttribute('aria-disabled', 'true'); // loading — aria-disabled (фокус не теряется)
    fireEvent.click(button);
    expect(retry).not.toHaveBeenCalled();
  });

  test('ошибка не от API (сеть) — общий текст без кода', () => {
    renderWithIntl(<ErrorState error={new TypeError('Failed to fetch')} onRetry={vi.fn()} />);
    expect(screen.getByRole('alert')).not.toHaveTextContent('Код ошибки');
  });
});

test('EmptyState: «Ничего не нашлось… Это не ошибка — просто пусто» и действие', () => {
  renderWithIntl(<EmptyState action={<button type="button">Сбросить фильтры</button>} />);
  expect(screen.getByText('Ничего не нашлось')).toBeInTheDocument();
  expect(
    screen.getByText('По этому запросу и фильтру записей нет. Это не ошибка — просто пусто.'),
  ).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Сбросить фильтры' })).toBeInTheDocument();
});

test('LoadingRows: скелетон строк с подписью для читалки', () => {
  renderWithIntl(<LoadingRows rows={4} />);
  const status = screen.getByRole('status');
  expect(status).toHaveTextContent('Загрузка…');
  expect(status.querySelectorAll('[data-skeleton-row]')).toHaveLength(4);
});

test('ForbiddenState — заголовок страницы', () => {
  renderWithIntl(<ForbiddenState />);
  expect(screen.getByRole('heading', { level: 1, name: 'Нет доступа к разделу' })).toBeVisible();
});

// Пометка вместо действия на карманном устройстве (спека UI-фундамента §3.2): текст, не кнопка;
// блок — заголовок и пояснение, compact — одна строка с иконкой.
test('DesktopOnlyNotice — блок и одна строка, без кнопок и заголовков страницы', () => {
  const { unmount } = renderWithIntl(<DesktopOnlyNotice />);
  expect(screen.getByText('Выдача и снятие прав — только с компьютера')).toBeVisible();
  expect(screen.getByText(/откройте админку на компьютере/i)).toBeVisible();
  expect(screen.queryByRole('heading')).toBeNull();
  expect(screen.queryByRole('button')).toBeNull();
  unmount();
  renderWithIntl(<DesktopOnlyNotice compact />);
  expect(screen.getByText('Выдача и снятие прав — только с компьютера')).toBeVisible();
  expect(screen.queryByText(/откройте админку на компьютере/i)).toBeNull();
});

test('PendingApprovalNotice — после 202: отправлено на подтверждение', () => {
  renderWithIntl(<PendingApprovalNotice />);
  expect(screen.getByRole('status')).toHaveTextContent('Отправлено на подтверждение');
});

test('ConnectionErrorState: «нет связи» и «Повторить»', () => {
  const retry = vi.fn();
  renderWithIntl(<ConnectionErrorState onRetry={retry} />);
  expect(screen.getByRole('alert')).toHaveTextContent('Нет связи с сервером');
  fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
  expect(retry).toHaveBeenCalledTimes(1);
});
