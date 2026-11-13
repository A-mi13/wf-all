import { screen } from '@testing-library/react';
import { describe, expect, test, vi } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { AttentionTile } from './AttentionTile';
import { attentionState } from './state';

// Плитка главной (спека UI-фундамента §4.13, план UF4.2): вся плитка — одна ссылка, имя = заголовок + число;
// «99+» — при value ≥ 100 или more (у ответа есть next_cursor: общего счёта у API нет); ошибка — «не загрузилось ·
// Повторить» внутри плитки (ссылки нет: кнопка в ссылке недопустима).

const base = {
  title: 'Ждут вашего решения',
  tone: 'warning' as const,
  href: '/approvals',
  onRetry: () => undefined,
  onNavigate: () => undefined,
};

describe('AttentionTile', () => {
  test.each([
    [3, false, '3'],
    [99, false, '99'],
    [100, false, '99+'],
    [99, true, '99+'],
    [0, false, '0'],
  ])('value %d, more %s — «%s» в имени ссылки', (value, more, text) => {
    renderWithIntl(<AttentionTile {...base} state="ready" value={value} more={more} />);
    const link = screen.getByRole('link', { name: `Ждут вашего решения ${text}` });
    expect(link).toHaveAttribute('href', '/approvals');
    expect(link).toHaveTextContent(text);
  });

  test('cap — свой порог', () => {
    renderWithIntl(<AttentionTile {...base} state="ready" value={10} cap={9} />);
    expect(screen.getByRole('link', { name: 'Ждут вашего решения 9+' })).toBeInTheDocument();
  });

  test('примечание — описание ссылки, не часть имени', () => {
    renderWithIntl(<AttentionTile {...base} state="ready" value={2} note="Решать с компьютера" />);
    const link = screen.getByRole('link', { name: 'Ждут вашего решения 2' });
    expect(link).toHaveAccessibleDescription('Решать с компьютера');
  });

  test('обычный клик — onNavigate; с Ctrl — браузер (onNavigate не зовётся)', async () => {
    const onNavigate = vi.fn();
    const { user } = renderWithIntl(
      <AttentionTile {...base} state="ready" value={1} onNavigate={onNavigate} />,
    );
    const link = screen.getByRole('link', { name: 'Ждут вашего решения 1' });
    await user.click(link);
    expect(onNavigate).toHaveBeenCalledTimes(1);
    await user.keyboard('{Control>}');
    await user.click(link);
    await user.keyboard('{/Control}');
    expect(onNavigate).toHaveBeenCalledTimes(1);
  });

  test('загрузка — ссылка с заголовком, числа нет', () => {
    renderWithIntl(<AttentionTile {...base} state="loading" value={null} />);
    expect(screen.getByRole('link', { name: 'Ждут вашего решения' })).toBeInTheDocument();
  });

  test('ошибка — «Не загрузилось» и «Повторить» внутри плитки, без ссылки', async () => {
    const onRetry = vi.fn();
    const { user } = renderWithIntl(
      <AttentionTile {...base} state="error" value={null} onRetry={onRetry} />,
    );
    expect(screen.queryByRole('link')).toBeNull();
    expect(screen.getByText('Ждут вашего решения')).toBeInTheDocument();
    expect(screen.getByText('Не загрузилось')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Повторить' }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});

test('удачный «Повторить» — фокус на ссылку плитки, а не на body', async () => {
  const { user, rerender } = renderWithIntl(<AttentionTile {...base} state="error" value={null} />);
  await user.click(screen.getByRole('button', { name: 'Повторить' }));
  rerender(<AttentionTile {...base} state="ready" value={2} />);
  expect(screen.getByRole('link', { name: 'Ждут вашего решения 2' })).toHaveFocus();
});

test('готово без нажатия «Повторить» — фокус не трогается', () => {
  const { rerender } = renderWithIntl(<AttentionTile {...base} state="error" value={null} />);
  rerender(<AttentionTile {...base} state="ready" value={2} />);
  expect(document.activeElement).toBe(document.body);
});

test('после «Повторить» фокус ушёл сам — готово его не перехватывает', async () => {
  const ui = (state: 'error' | 'ready') => (
    <>
      <AttentionTile {...base} state={state} value={state === 'ready' ? 2 : null} />
      <input aria-label="Поиск" />
    </>
  );
  const { user, rerender } = renderWithIntl(ui('error'));
  await user.click(screen.getByRole('button', { name: 'Повторить' }));
  await user.click(screen.getByRole('textbox', { name: 'Поиск' }));
  rerender(ui('ready'));
  expect(screen.getByRole('textbox', { name: 'Поиск' })).toHaveFocus();
});

test('attentionState: данных нет — загрузка или ошибка; данные есть — готово и при фоновой ошибке', () => {
  expect(attentionState({ data: undefined, isError: false })).toBe('loading');
  expect(attentionState({ data: undefined, isError: true })).toBe('error');
  expect(attentionState({ data: { items: [] }, isError: true })).toBe('ready');
});
