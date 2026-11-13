import { fireEvent, screen } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, test, vi } from 'vitest';
import { renderWithIntl } from '../test/render';
import { Switch } from './Switch';

// Переключатель (флаги функций, включение страны — план B Task 17): role="switch" и aria-checked,
// клик и клавиатура (Space, Enter) меняют; disabled и busy (запрос в полёте) — нет; busy держит
// фокус (aria-disabled, не disabled — как Button loading).

function Controlled(props: {
  disabled?: boolean;
  busy?: boolean;
  onChange?: (v: boolean) => void;
}) {
  const [on, setOn] = useState(false);
  return (
    <Switch
      checked={on}
      onChange={(v) => {
        props.onChange?.(v);
        setOn(v);
      }}
      label="Подписки"
      disabled={props.disabled}
      busy={props.busy}
    />
  );
}

describe('Switch', () => {
  test('роль switch, имя — подпись, aria-checked следует за значением', async () => {
    const { user } = renderWithIntl(<Controlled />);
    const sw = screen.getByRole('switch', { name: 'Подписки' });
    expect(sw).toHaveAttribute('aria-checked', 'false');
    await user.click(sw);
    expect(sw).toHaveAttribute('aria-checked', 'true');
    await user.click(sw);
    expect(sw).toHaveAttribute('aria-checked', 'false');
  });

  test('клавиатура: Space и Enter переключают', async () => {
    const onChange = vi.fn();
    const { user } = renderWithIntl(<Controlled onChange={onChange} />);
    const sw = screen.getByRole('switch', { name: 'Подписки' });
    sw.focus();
    await user.keyboard(' ');
    expect(sw).toHaveAttribute('aria-checked', 'true');
    await user.keyboard('{Enter}');
    expect(sw).toHaveAttribute('aria-checked', 'false');
    expect(onChange.mock.calls).toEqual([[true], [false]]);
  });

  test('disabled — не меняется ни кликом, ни клавиатурой', async () => {
    const onChange = vi.fn();
    const { user } = renderWithIntl(<Controlled disabled onChange={onChange} />);
    const sw = screen.getByRole('switch', { name: 'Подписки' });
    expect(sw).toBeDisabled();
    await user.click(sw);
    fireEvent.keyDown(sw, { key: ' ' });
    expect(onChange).not.toHaveBeenCalled();
    expect(sw).toHaveAttribute('aria-checked', 'false');
  });

  test('busy — запрос в полёте: aria-busy и aria-disabled, фокус остаётся, переключение гасится', async () => {
    const onChange = vi.fn();
    const { user } = renderWithIntl(<Controlled busy onChange={onChange} />);
    const sw = screen.getByRole('switch', { name: 'Подписки' });
    expect(sw).not.toBeDisabled();
    expect(sw).toHaveAttribute('aria-busy', 'true');
    expect(sw).toHaveAttribute('aria-disabled', 'true');
    sw.focus();
    await user.click(sw);
    await user.keyboard(' ');
    await user.keyboard('{Enter}');
    expect(onChange).not.toHaveBeenCalled();
    expect(sw).toHaveFocus();
    expect(sw).toHaveAttribute('aria-checked', 'false');
  });

  test('hideLabel — подпись только для читалки (ячейка матрицы флагов)', () => {
    renderWithIntl(<Switch checked label="Подписки · Ставрополь" hideLabel onChange={() => {}} />);
    const sw = screen.getByRole('switch', { name: 'Подписки · Ставрополь' });
    expect(sw).toHaveAttribute('aria-checked', 'true');
  });
});
