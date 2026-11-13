import { fireEvent, screen, within } from '@testing-library/react';
import { ICONS } from '@wf/icons';
import { useState } from 'react';
import { useForm } from 'react-hook-form';
import { describe, expect, test, vi } from 'vitest';
import { stubMedia } from '../../test/media';
import { openOptions, pickMany } from '../../test/pick';
import { renderWithIntl } from '../../test/render';
import { Drawer } from '../overlay/Drawer';
import { Form } from './Form';
import { MultiSelectField } from './MultiSelectField';
import list from './Select.module.css';

// Мультивыбор на Popover/Sheet (спека UI-фундамента §4.16): список role="listbox" aria-multiselectable,
// опция отмечается и снимается, список остаётся открытым; без поиска якорь — кнопка combobox.

const CITIES = [
  { value: 'c1', label: 'Ставрополь' },
  { value: 'c2', label: 'Михайловск' },
  { value: 'c3', label: 'Невинномысск', disabled: true },
];

function Cities({
  searchable,
  onValue,
}: {
  searchable?: boolean;
  onValue?: (v: string[]) => void;
}) {
  const form = useForm({ defaultValues: { city_ids: [] as string[] } });
  return (
    <Form form={form} onSubmit={() => undefined}>
      <MultiSelectField
        name="city_ids"
        label="Города"
        options={CITIES}
        searchable={searchable}
        rules={{ onChange: (e: { target: { value: string[] } }) => onValue?.(e.target.value) }}
      />
    </Form>
  );
}

const combo = () => screen.getByRole('combobox', { name: 'Города' });

describe('MultiSelectField', () => {
  test('кнопка-combobox: клик отмечает и снимает, список открыт; недоступная не отмечается', async () => {
    const onValue = vi.fn();
    const { user } = renderWithIntl(<Cities onValue={onValue} />);
    expect(combo()).toHaveTextContent('Выбрать');
    expect(screen.queryByRole('listbox')).toBeNull();
    await user.click(combo());
    const list = screen.getByRole('listbox', { name: 'Города' });
    expect(list).toHaveAttribute('aria-multiselectable', 'true');
    await user.click(within(list).getByRole('option', { name: 'Михайловск' }));
    await user.click(within(list).getByRole('option', { name: 'Ставрополь' }));
    await user.click(within(list).getByRole('option', { name: 'Невинномысск' }));
    expect(screen.getByRole('listbox')).toBeVisible();
    expect(onValue).toHaveBeenLastCalledWith(['c2', 'c1']);
    await user.click(within(list).getByRole('option', { name: 'Михайловск' }));
    expect(onValue).toHaveBeenLastCalledWith(['c1']);
    expect(combo()).toHaveTextContent('Выбрано: 1');
    expect(combo()).toHaveFocus();
  });

  // Крестик чипа — значок close из @wf/icons, не свой контур (принцип 7; ревью UF2a/II-A, m7).
  test('крестик чипа «Убрать» — значок close набора', async () => {
    const { user } = renderWithIntl(<Cities />);
    await user.click(combo());
    await user.click(screen.getByRole('option', { name: 'Ставрополь' }));
    const remove = screen.getByRole('button', { name: 'Убрать «Ставрополь»' });
    expect(remove.querySelector('path')).toHaveAttribute('d', ICONS.close.layers[0]?.d);
    // прежний размер крестика чипа — 12 px (раунд 1 ревью хвостов UI, m2)
    expect(remove.querySelector('svg')).toHaveAttribute('width', '12');
  });

  test('клавиатура: ↓ открывает, Space отмечает активную, Esc закрывает, фокус на кнопке', async () => {
    const onValue = vi.fn();
    const { user } = renderWithIntl(<Cities onValue={onValue} />);
    combo().focus();
    await user.keyboard('{ArrowDown}');
    expect(screen.getByRole('listbox')).toBeVisible();
    await user.keyboard('{ArrowDown} ');
    expect(onValue).toHaveBeenLastCalledWith(['c2']);
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(combo()).toHaveFocus();
  });

  // Firefox шлёт click кнопки на keyup Space после отменённого keydown (ревью UF2a/II-A, m2)
  test('click после keyup Space не закрывает только что открытый список; настоящий щелчок — закрывает', () => {
    renderWithIntl(<Cities />);
    const c = combo();
    c.focus();
    fireEvent.keyDown(c, { key: ' ' });
    fireEvent.keyUp(c, { key: ' ' });
    fireEvent.click(c, { detail: 0 });
    expect(screen.getByRole('listbox')).toBeVisible();
    fireEvent.click(c, { detail: 1 });
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  test('в Drawer: Esc закрывает только список; второй — панель', async () => {
    function Panel() {
      const [open, setOpen] = useState(true);
      return (
        <Drawer open={open} onClose={() => setOpen(false)} title="Роль и города">
          <Cities />
        </Drawer>
      );
    }
    const { user } = renderWithIntl(<Panel />);
    await openOptions(user, combo());
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(screen.getByRole('dialog', { name: 'Роль и города' })).toBeInTheDocument();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  test('pickMany: отметить несколько и закрыть', async () => {
    const onValue = vi.fn();
    const { user } = renderWithIntl(<Cities searchable onValue={onValue} />);
    await pickMany(user, 'Города: поиск', ['Ставрополь', 'Михайловск']);
    expect(onValue).toHaveBeenLastCalledWith(['c1', 'c2']);
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  test('телефон: шторка с подписью и поиском; закрытие — фокус на кнопке, даже если нажатие её не фокусировало', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    const onValue = vi.fn();
    const { user } = renderWithIntl(<Cities searchable onValue={onValue} />);
    // Safari: нажатие кнопку не фокусирует — открыватель шторки не запомнен
    fireEvent.click(combo());
    const sheet = screen.getByRole('dialog', { name: 'Города' });
    await user.type(within(sheet).getByRole('combobox', { name: 'Города: поиск' }), 'мих');
    // поиск закреплён сверху тела шторки (ревью UF2a/II-A, I2)
    expect(
      within(sheet)
        .getByRole('combobox', { name: 'Города: поиск' })
        .closest(`.${list.sheetSearch}`),
    ).not.toBeNull();
    fireEvent.click(within(sheet).getByRole('option', { name: 'Михайловск' }));
    expect(onValue).toHaveBeenLastCalledWith(['c2']);
    fireEvent.click(within(sheet).getByRole('button', { name: 'Закрыть' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(combo()).toHaveFocus();
  });
});
