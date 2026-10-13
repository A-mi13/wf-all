import { act, fireEvent, screen } from '@testing-library/react';
import { createRef } from 'react';
import { describe, expect, test, vi } from 'vitest';
import { renderWithIntl } from '../test/render';
import { OtpInput, type OtpInputHandle } from './OtpInput';

// Review Focus 5 (фронт): код второго фактора вводится по ячейкам, вставляется из буфера в любом
// виде («123 456», «123-456»), уходит ровно один раз и не принимает ничего, кроме цифр.

const cells = () => screen.getAllByRole('textbox');
const values = () => cells().map((c) => (c as HTMLInputElement).value);

function setup(props: Partial<Parameters<typeof OtpInput>[0]> = {}) {
  const onComplete = vi.fn();
  const ref = createRef<OtpInputHandle>();
  const r = renderWithIntl(
    <OtpInput ref={ref} label="Код из приложения" onComplete={onComplete} {...props} />,
  );
  return { ...r, onComplete, ref };
}

describe('OtpInput', () => {
  test('6 ячеек в группе: цифровая клавиатура и автозаполнение кода', () => {
    setup();
    expect(screen.getByRole('group', { name: 'Код из приложения' })).toBeInTheDocument();
    expect(cells()).toHaveLength(6);
    for (const [i, c] of cells().entries()) {
      expect(c).toHaveAttribute('inputmode', 'numeric');
      expect(c).toHaveAttribute('autocomplete', 'one-time-code');
      expect(c).toHaveAccessibleName(`Цифра ${i + 1} из 6`);
    }
  });

  test('ввод по ячейкам: фокус идёт дальше, полный код уходит один раз', async () => {
    const { user, onComplete } = setup();
    await user.click(cells()[0]!);
    await user.keyboard('12345');
    expect(cells()[5]).toHaveFocus();
    expect(onComplete).not.toHaveBeenCalled();
    await user.keyboard('6');
    expect(values()).toEqual(['1', '2', '3', '4', '5', '6']);
    expect(onComplete).toHaveBeenCalledTimes(1);
    expect(onComplete).toHaveBeenCalledWith('123456');
  });

  test.each(['123 456', '123-456', ' 12 34 56\n'])(
    'вставка «%s» → onComplete("123456") один раз',
    async (text) => {
      const { user, onComplete } = setup();
      await user.click(cells()[0]!);
      await user.paste(text);
      expect(values()).toEqual(['1', '2', '3', '4', '5', '6']);
      expect(onComplete).toHaveBeenCalledTimes(1);
      expect(onComplete).toHaveBeenCalledWith('123456');
    },
  );

  test('вставка полного кода в среднюю ячейку заполняет все с первой', async () => {
    const { user, onComplete } = setup();
    await user.click(cells()[3]!);
    await user.paste('654321');
    expect(values()).toEqual(['6', '5', '4', '3', '2', '1']);
    expect(onComplete).toHaveBeenCalledExactlyOnceWith('654321');
  });

  test('автозаполнение кода целиком в одну ячейку (one-time-code)', () => {
    const { onComplete } = setup();
    fireEvent.change(cells()[0]!, { target: { value: '987654' } });
    expect(values()).toEqual(['9', '8', '7', '6', '5', '4']);
    expect(onComplete).toHaveBeenCalledExactlyOnceWith('987654');
  });

  test('не-цифры игнорируются: при вводе и при вставке', async () => {
    const { user, onComplete } = setup();
    await user.click(cells()[0]!);
    await user.keyboard('a-1 b2');
    expect(values()).toEqual(['1', '2', '', '', '', '']);
    await user.paste('abc');
    expect(values()).toEqual(['1', '2', '', '', '', '']);
    fireEvent.change(cells()[2]!, { target: { value: 'x' } });
    expect(values()).toEqual(['1', '2', '', '', '', '']);
    expect(onComplete).not.toHaveBeenCalled();
  });

  test('Backspace: в заполненной ячейке стирает её, в пустой уходит назад и стирает предыдущую', async () => {
    const { user } = setup();
    await user.click(cells()[0]!);
    await user.keyboard('123');
    expect(cells()[3]).toHaveFocus();
    await user.keyboard('{Backspace}');
    expect(cells()[2]).toHaveFocus();
    expect(values()).toEqual(['1', '2', '', '', '', '']);
    await user.click(cells()[1]!);
    await user.keyboard('{Backspace}');
    expect(cells()[1]).toHaveFocus();
    expect(values()).toEqual(['1', '', '', '', '', '']);
  });

  test('стрелки, Home и End переводят фокус', async () => {
    const { user } = setup();
    await user.click(cells()[2]!);
    await user.keyboard('{ArrowLeft}');
    expect(cells()[1]).toHaveFocus();
    await user.keyboard('{ArrowRight}{ArrowRight}');
    expect(cells()[3]).toHaveFocus();
    await user.keyboard('{End}');
    expect(cells()[5]).toHaveFocus();
    await user.keyboard('{ArrowRight}');
    expect(cells()[5]).toHaveFocus();
    await user.keyboard('{Home}');
    expect(cells()[0]).toHaveFocus();
    await user.keyboard('{ArrowLeft}');
    expect(cells()[0]).toHaveFocus();
  });

  test('reset() после ошибки: ячейки пусты, фокус на первой, следующий код снова уходит', async () => {
    const { user, onComplete, ref } = setup();
    await user.click(cells()[0]!);
    await user.paste('111111');
    expect(onComplete).toHaveBeenCalledTimes(1);
    act(() => ref.current?.reset());
    expect(values()).toEqual(['', '', '', '', '', '']);
    expect(cells()[0]).toHaveFocus();
    await user.keyboard('222222');
    expect(onComplete).toHaveBeenCalledTimes(2);
    expect(onComplete).toHaveBeenLastCalledWith('222222');
  });

  // Реальный сценарий: onError мутации зовёт reset(), пока ячейки ещё disabled (isPending), —
  // фокус ставится, когда их включат.
  test.each([
    ['reset()', (h: OtpInputHandle) => h.reset()],
    [
      'reset() и focus()',
      (h: OtpInputHandle) => {
        h.reset();
        h.focus();
      },
    ],
  ] as const)('%s при disabled: фокус — после включения ячеек', async (_, call) => {
    const onComplete = vi.fn();
    const ref = createRef<OtpInputHandle>();
    const ui = (disabled: boolean) => (
      <OtpInput ref={ref} label="Код из приложения" onComplete={onComplete} disabled={disabled} />
    );
    const { user, rerender } = renderWithIntl(ui(false));
    await user.click(cells()[0]!);
    await user.paste('123456');
    rerender(ui(true));
    act(() => call(ref.current!));
    expect(values()).toEqual(['', '', '', '', '', '']);
    expect(cells()[0]).not.toHaveFocus();
    rerender(ui(false));
    expect(cells()[0]).toHaveFocus();
  });

  test('ошибка помечает группу, disabled выключает ячейки', () => {
    const { rerender } = setup({ invalid: true });
    for (const c of cells()) expect(c).toHaveAttribute('aria-invalid', 'true');
    rerender(<OtpInput label="Код из приложения" onComplete={() => {}} disabled />);
    for (const c of cells()) expect(c).toBeDisabled();
  });
});
