import { act, fireEvent, screen } from '@testing-library/react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { renderWithIntl } from '../test/render';
import { CopyButton } from './CopyButton';

// Копирование в один клик (спека UI-фундамента §4.8): успех — значок «галочка» на 2 с и «Скопировано» в
// вежливой живой области (без тоста); отказ (нет разрешения, не HTTPS, нет API) — значение выделено в
// поле только для чтения рядом и тост «Скопируйте вручную».

afterEach(() => {
  vi.useRealTimers();
  Reflect.deleteProperty(navigator, 'clipboard');
});

// Clipboard API — своё свойство navigator на время теста (undefined — API нет: не HTTPS). Ставится
// ПОСЛЕ renderWithIntl: userEvent.setup() подкладывает свою заглушку буфера обмена.
function clipboard(writeText: ((v: string) => Promise<void>) | undefined) {
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: writeText ? { writeText } : undefined,
  });
}

const icon = () => document.querySelector('[data-copy-icon]')?.getAttribute('data-copy-icon');
const live = () => document.querySelector('[data-copy-live]');

describe('CopyButton', () => {
  test('успех: значок check и «Скопировано» в живой области на 2 с, затем обратно; повтор — снова', async () => {
    vi.useFakeTimers();
    const writeText = vi.fn(() => Promise.resolve());
    renderWithIntl(<CopyButton value="0192f1c4-1111" label="Скопировать id" />);
    clipboard(writeText);
    const button = screen.getByRole('button', { name: 'Скопировать id' });
    expect(icon()).toBe('copy');
    expect(live()).toHaveAttribute('aria-live', 'polite');
    await act(async () => fireEvent.click(button));
    expect(writeText).toHaveBeenCalledWith('0192f1c4-1111');
    expect(icon()).toBe('check');
    expect(live()).toHaveTextContent('Скопировано');
    expect(screen.queryByText('Скопируйте вручную')).toBeNull();
    act(() => vi.advanceTimersByTime(1999));
    expect(icon()).toBe('check');
    act(() => vi.advanceTimersByTime(1));
    expect(icon()).toBe('copy');
    expect(live()).toHaveTextContent('');
    // повтор после 2 с — снова успех
    await act(async () => fireEvent.click(button));
    expect(writeText).toHaveBeenCalledTimes(2);
    expect(icon()).toBe('check');
  });

  test('отказ writeText — поле только для чтения с выделенным значением и тост «Скопируйте вручную»', async () => {
    renderWithIntl(<CopyButton value="ivan@xn--d1acufc.xn--p1ai" label="Скопировать почту" />);
    clipboard(() => Promise.reject(new DOMException('denied', 'NotAllowedError')));
    await act(async () =>
      fireEvent.click(screen.getByRole('button', { name: 'Скопировать почту' })),
    );
    const field = await screen.findByRole('textbox', { name: 'Скопировать почту' });
    expect(field).toHaveAttribute('readonly');
    expect(field).toHaveValue('ivan@xn--d1acufc.xn--p1ai');
    expect(field).toHaveFocus();
    const input = field as HTMLInputElement;
    expect([input.selectionStart, input.selectionEnd]).toEqual([0, input.value.length]);
    expect(await screen.findByText('Скопируйте вручную')).toBeInTheDocument();
    expect(icon()).toBe('copy');
    expect(live()).toHaveTextContent('');
  });

  // UF2a.10: резервные коды — по строке на код; однострочное поле съело бы переводы строк, и коды вручную
  // скопировались бы слитно.
  test('отказ на многострочном значении — многострочное поле, строки целы', async () => {
    renderWithIntl(<CopyButton value={'aaaaa-11111\nbbbbb-22222'} label="Копировать: коды" />);
    clipboard(() => Promise.reject(new DOMException('denied', 'NotAllowedError')));
    await act(async () =>
      fireEvent.click(screen.getByRole('button', { name: 'Копировать: коды' })),
    );
    const field = await screen.findByRole('textbox', { name: 'Копировать: коды' });
    expect(field.tagName).toBe('TEXTAREA');
    expect(field).toHaveValue('aaaaa-11111\nbbbbb-22222');
    expect(field).toHaveAttribute('readonly');
    expect(field).toHaveFocus();
  });

  // Ревью UF2a/I, m8: тост-ошибку закрывают только руками — повторные отказы не копят стопку тостов,
  // поле уже на экране: значение выделяется снова.
  test('повторный отказ — второго тоста нет, значение снова выделено', async () => {
    renderWithIntl(<CopyButton value="12345" label="Скопировать id" />);
    clipboard(() => Promise.reject(new DOMException('denied', 'NotAllowedError')));
    const button = screen.getByRole('button', { name: 'Скопировать id' });
    await act(async () => fireEvent.click(button));
    await act(async () => fireEvent.click(button));
    expect(screen.getAllByText('Скопируйте вручную')).toHaveLength(1);
    expect(screen.getByRole('textbox', { name: 'Скопировать id' })).toHaveFocus();
  });

  test('preview — состояние для витрины без нажатия: «галочка» или поле отказа', () => {
    const { unmount } = renderWithIntl(
      <CopyButton value="x" label="Скопировать" preview="copied" />,
    );
    expect(icon()).toBe('check');
    unmount();
    renderWithIntl(<CopyButton value="12345" label="Скопировать" preview="manual" />);
    expect(screen.getByRole('textbox', { name: 'Скопировать' })).toHaveValue('12345');
  });

  test('нет Clipboard API (не HTTPS) — тот же отказ', async () => {
    renderWithIntl(<CopyButton value="12345" label="Скопировать geoname_id" display="text" />);
    clipboard(undefined);
    const button = screen.getByRole('button', { name: 'Скопировать geoname_id' });
    expect(button).toHaveTextContent('Скопировать geoname_id');
    await act(async () => fireEvent.click(button));
    expect(await screen.findByRole('textbox', { name: 'Скопировать geoname_id' })).toHaveValue(
      '12345',
    );
  });

  test('размонтирование до конца 2 с — таймер снят', async () => {
    vi.useFakeTimers();
    const { unmount } = renderWithIntl(<CopyButton value="x" label="Скопировать" />);
    clipboard(() => Promise.resolve());
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Скопировать' })));
    expect(vi.getTimerCount()).toBeGreaterThan(0);
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
