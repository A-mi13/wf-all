import { createEvent, fireEvent, screen, within } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { stubMedia } from '../../test/media';
import { openOptions, pick, selectedText } from '../../test/pick';
import { renderWithIntl } from '../../test/render';
import { Drawer } from '../overlay/Drawer';
import { Select } from './Select';
import styles from './Select.module.css';
import type { Option } from './SelectField';

// Свой выпадающий список (спека UI-фундамента §4.16): шаблон WAI-ARIA «select-only combobox» — кнопка
// role="combobox" с подписью поля в имени, список на Popover (широкий экран) или в Sheet (телефон).

const REGIONS: Option[] = [
  { value: '', label: 'Без региона' },
  { value: 'sverd', label: 'Свердловская область' },
  { value: 'stav', label: 'Ставропольский край' },
  { value: 'sakh', label: 'Сахалинская область', disabled: true },
  { value: 'sam', label: 'Самарская область' },
  { value: 'mos', label: 'Московская область' },
];

function Harness({
  options = REGIONS,
  initial = '',
  onChange,
  placeholder,
}: {
  options?: Option[];
  initial?: string;
  onChange?: (v: string) => void;
  placeholder?: string;
}) {
  const [value, setValue] = useState(initial);
  return (
    <>
      <span id="region-label">Регион</span>
      <Select
        id="region"
        name="region"
        value={value}
        onChange={(v) => {
          setValue(v);
          onChange?.(v);
        }}
        onBlur={() => undefined}
        options={options}
        placeholder={placeholder}
        labelledBy="region-label"
        label="Регион"
      />
      <button type="button">Дальше</button>
    </>
  );
}

const combo = () => screen.getByRole('combobox', { name: 'Регион' });
const active = () => {
  const id = combo().getAttribute('aria-activedescendant');
  return id ? document.getElementById(id) : null;
};

afterEach(() => {
  vi.restoreAllMocks();
});

describe('Select: доступность', () => {
  test('имя — только подпись поля, значение — текст кнопки; закрытый — опций нет в дереве', () => {
    renderWithIntl(<Harness initial="stav" />);
    const c = combo();
    expect(c).toHaveAccessibleName('Регион');
    expect(c).toHaveTextContent('Ставропольский край');
    expect(selectedText(c)).toBe('Ставропольский край');
    expect(c).toHaveAttribute('aria-expanded', 'false');
    expect(c).not.toHaveAttribute('aria-activedescendant');
    expect(screen.queryAllByRole('option')).toEqual([]);
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  test('открыт: listbox по aria-controls, активная — выбранная, aria-selected, data-value, aria-disabled', async () => {
    const { user } = renderWithIntl(<Harness initial="stav" />);
    await user.click(combo());
    const list = screen.getByRole('listbox', { name: 'Регион' });
    expect(list).toBeVisible();
    expect(combo()).toHaveAttribute('aria-expanded', 'true');
    expect(combo()).toHaveAttribute('aria-controls', list.id);
    expect(active()).toHaveTextContent('Ставропольский край');
    const options = within(list).getAllByRole('option');
    expect(options.map((o) => o.getAttribute('data-value'))).toEqual(REGIONS.map((o) => o.value));
    expect(within(list).getByRole('option', { name: 'Ставропольский край' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
    expect(within(list).getByRole('option', { name: 'Без региона' })).toHaveAttribute(
      'aria-selected',
      'false',
    );
    expect(within(list).getByRole('option', { name: 'Сахалинская область' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
    expect(combo()).toHaveFocus();
  });

  test('openOptions — значения, подписи и недоступность опций; Esc закрывает', async () => {
    const { user } = renderWithIntl(<Harness />);
    const options = await openOptions(user, 'Регион');
    expect(options.map(({ value, label, disabled }) => ({ value, label, disabled }))).toEqual(
      REGIONS.map((o) => ({ value: o.value, label: o.label, disabled: Boolean(o.disabled) })),
    );
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  test('pick — по подписи и по значению; placeholder — когда ничего не выбрано', async () => {
    const onChange = vi.fn();
    const { user } = renderWithIntl(
      <Harness onChange={onChange} options={REGIONS.slice(1)} placeholder="Выберите регион" />,
    );
    expect(selectedText(combo())).toBe('Выберите регион');
    await pick(user, 'Регион', 'Самарская область');
    expect(selectedText(combo())).toBe('Самарская область');
    await pick(user, combo(), 'mos');
    expect(onChange.mock.calls).toEqual([['sam'], ['mos']]);
  });
});

describe('Select: клавиатура', () => {
  test('↓ открывает на выбранной; ↓/↑/Home/End двигают активную, пропуская недоступные; Enter выбирает', async () => {
    const onChange = vi.fn();
    const { user } = renderWithIntl(<Harness initial="stav" onChange={onChange} />);
    combo().focus();
    await user.keyboard('{ArrowDown}');
    expect(screen.getByRole('listbox')).toBeVisible();
    expect(active()).toHaveTextContent('Ставропольский край');
    await user.keyboard('{ArrowDown}');
    expect(active()).toHaveTextContent('Самарская область'); // Сахалинская недоступна
    await user.keyboard('{ArrowUp}');
    expect(active()).toHaveTextContent('Ставропольский край');
    await user.keyboard('{Home}');
    expect(active()).toHaveTextContent('Без региона');
    await user.keyboard('{ArrowUp}'); // у края — остаётся
    expect(active()).toHaveTextContent('Без региона');
    await user.keyboard('{End}');
    expect(active()).toHaveTextContent('Московская область');
    await user.keyboard('{Enter}');
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(onChange).toHaveBeenCalledWith('mos');
    expect(selectedText(combo())).toBe('Московская область');
    expect(combo()).toHaveFocus();
  });

  test('Space открывает и выбирает; Esc закрывает без выбора, фокус на кнопке', async () => {
    const onChange = vi.fn();
    const { user } = renderWithIntl(<Harness initial="stav" onChange={onChange} />);
    combo().focus();
    await user.keyboard(' ');
    await user.keyboard('{ArrowDown}');
    await user.keyboard(' ');
    expect(onChange).toHaveBeenCalledWith('sam');
    await user.keyboard('{Enter}');
    await user.keyboard('{ArrowUp}');
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(selectedText(combo())).toBe('Самарская область');
    expect(combo()).toHaveFocus();
  });

  // Firefox шлёт click кнопки на keyup Space даже после отменённого keydown: список, открытый на keydown,
  // не закрывается этим click; следующий настоящий click — снова переключает.
  test('click после keyup Space/Enter не переключает список второй раз', () => {
    renderWithIntl(<Harness />);
    const c = combo();
    c.focus();
    fireEvent.keyDown(c, { key: ' ' });
    fireEvent.keyUp(c, { key: ' ' });
    fireEvent.click(c, { detail: 0 });
    expect(screen.getByRole('listbox')).toBeVisible();
  });

  test('Esc на закрытом не гасится (закроет панель под ним)', () => {
    renderWithIntl(<Harness />);
    const down = createEvent.keyDown(combo(), { key: 'Escape' });
    fireEvent(combo(), down);
    expect(down.defaultPrevented).toBe(false);
  });

  test('Tab закрывает без выбора и уводит фокус дальше', async () => {
    const onChange = vi.fn();
    const { user } = renderWithIntl(<Harness initial="stav" onChange={onChange} />);
    await user.click(combo());
    await user.keyboard('{ArrowDown}');
    await user.tab();
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Дальше' })).toHaveFocus();
  });

  test('typeahead: буква открывает и ведёт к опции; повтор — к следующей на ту же; недоступные пропущены', async () => {
    const { user } = renderWithIntl(<Harness />);
    combo().focus();
    await user.keyboard('с');
    expect(screen.getByRole('listbox')).toBeVisible();
    expect(active()).toHaveTextContent('Свердловская область');
    await user.keyboard('с');
    expect(active()).toHaveTextContent('Ставропольский край');
    await user.keyboard('с'); // Сахалинская недоступна
    expect(active()).toHaveTextContent('Самарская область');
    await user.keyboard('с'); // по кругу
    expect(active()).toHaveTextContent('Свердловская область');
  });

  test('typeahead: несколько букв подряд — по началу строки; нет совпадения — активная прежняя', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      const { user } = renderWithIntl(<Harness />);
      await user.click(combo());
      await user.keyboard('мо');
      expect(active()).toHaveTextContent('Московская область');
      vi.advanceTimersByTime(1000);
      await user.keyboard('щ');
      expect(active()).toHaveTextContent('Московская область');
    } finally {
      vi.useRealTimers();
    }
  });

  test('пустой список: открывается, Enter ничего не выбирает', async () => {
    const onChange = vi.fn();
    const { user } = renderWithIntl(<Harness options={[]} onChange={onChange} />);
    combo().focus();
    await user.keyboard('{ArrowDown}');
    expect(screen.getByRole('listbox')).toBeVisible();
    expect(combo()).not.toHaveAttribute('aria-activedescendant');
    await user.keyboard('{Enter}');
    expect(onChange).not.toHaveBeenCalled();
  });

  test('длинный список: активная прокручивается в видимую область (nearest)', async () => {
    const scroll = vi.fn();
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      configurable: true,
      value: scroll,
    });
    try {
      const many = Array.from({ length: 250 }, (_, i) => ({
        value: `c${i}`,
        label: `Страна ${i}`,
      }));
      const { user } = renderWithIntl(<Harness options={many} />);
      combo().focus();
      await user.keyboard('{ArrowDown}{End}');
      expect(active()).toHaveTextContent('Страна 249');
      expect(scroll).toHaveBeenLastCalledWith({ block: 'nearest' });
      expect(scroll.mock.contexts.at(-1)).toBe(active());
    } finally {
      Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView');
    }
  });
});

describe('Select: мышь', () => {
  // Страж R4-1 (спека §4.8): mousedown по нефокусируемой опции увёл бы фокус с combobox, focusout скрыл
  // бы пузырь до click — выбор мышью терялся бы.
  test('страж R4-1: mousedown по опции отменён, фокус на combobox, клик выбирает', async () => {
    const onChange = vi.fn();
    const { user } = renderWithIntl(<Harness onChange={onChange} />);
    await user.click(combo());
    const option = screen.getByRole('option', { name: 'Самарская область' });
    const down = createEvent.mouseDown(option);
    fireEvent(option, down);
    expect(down.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(combo());
    await user.click(option);
    expect(onChange).toHaveBeenCalledWith('sam');
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  test('недоступная опция кликом не выбирается, список открыт', async () => {
    const onChange = vi.fn();
    const { user } = renderWithIntl(<Harness onChange={onChange} />);
    await user.click(combo());
    await user.click(screen.getByRole('option', { name: 'Сахалинская область' }));
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByRole('listbox')).toBeVisible();
  });

  test('недоступное поле не открывается', async () => {
    const { user } = renderWithIntl(
      <>
        <span id="l">Регион</span>
        <Select
          id="r"
          name="r"
          value=""
          onChange={vi.fn()}
          onBlur={vi.fn()}
          options={REGIONS}
          labelledBy="l"
          label="Регион"
          disabled
        />
      </>,
    );
    expect(combo()).toBeDisabled();
    await user.click(combo());
    expect(screen.queryByRole('listbox')).toBeNull();
  });
});

describe('Select на телефоне — шторка', () => {
  const TWELVE: Option[] = Array.from({ length: 12 }, (_, i) => ({
    value: `v${i}`,
    label:
      [
        'Москва',
        'Мурманск',
        'Самара',
        'Саратов',
        'Сочи',
        'Тверь',
        'Тула',
        'Омск',
        'Орёл',
        'Пенза',
        'Пермь',
        'Псков',
      ][i] ?? '',
  }));

  test('шторка с подписью поля, > 10 опций — поиск; выбор закрывает, фокус на combobox', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    const onChange = vi.fn();
    const { user } = renderWithIntl(<Harness options={TWELVE} onChange={onChange} />);
    await user.click(combo());
    const sheet = screen.getByRole('dialog', { name: 'Регион' });
    expect(combo()).toHaveAttribute('aria-expanded', 'true');
    const search = within(sheet).getByRole('searchbox');
    await user.type(search, 'са');
    const list = within(sheet).getByRole('listbox', { name: 'Регион' });
    expect(
      within(list)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['Самара', 'Саратов']);
    await user.click(within(list).getByRole('option', { name: 'Саратов' }));
    expect(onChange).toHaveBeenCalledWith('v3');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(combo()).toHaveFocus();
    expect(combo()).toHaveAttribute('aria-expanded', 'false');
  });

  test('шторка открыта нажатием без фокуса (Safari) — после выбора фокус всё равно на combobox', () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    renderWithIntl(<Harness />);
    fireEvent.click(combo());
    expect(combo()).not.toHaveFocus();
    fireEvent.click(screen.getByRole('option', { name: 'Самарская область' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(combo()).toHaveFocus();
  });

  // Поиск длинного списка не уезжает при прокрутке к выбранной: закреплён сверху тела шторки (ревью
  // UF2a/II-A, I2; раскладку jsdom не считает — вид сторожит история LongList на 390).
  test('поиск шторки закреплён (sticky-обёртка), список — вне её', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    const { user } = renderWithIntl(<Harness options={TWELVE} />);
    await user.click(combo());
    const sheet = screen.getByRole('dialog', { name: 'Регион' });
    const pinned = within(sheet).getByRole('searchbox').closest(`.${styles.sheetSearch}`);
    expect(pinned).not.toBeNull();
    expect(pinned).not.toContainElement(within(sheet).getByRole('listbox'));
  });

  // Enter открыл шторку, фокус ушёл в неё — keyup до кнопки не дошёл (ревью UF2a/II-A, m1)
  test('после выбора в шторке, открытой Enter без keyup, первый щелчок мышью открывает снова', () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    renderWithIntl(<Harness />);
    combo().focus();
    fireEvent.keyDown(combo(), { key: 'Enter' });
    fireEvent.click(screen.getByRole('option', { name: 'Самарская область' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    fireEvent.click(combo(), { detail: 1 });
    expect(screen.getByRole('dialog', { name: 'Регион' })).toBeInTheDocument();
  });

  test('≤ 10 опций — без поиска; выбранная отмечена', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    const { user } = renderWithIntl(<Harness initial="stav" />);
    await user.click(combo());
    const sheet = screen.getByRole('dialog', { name: 'Регион' });
    expect(within(sheet).queryByRole('searchbox')).toBeNull();
    expect(within(sheet).getByRole('option', { name: 'Ставропольский край' })).toHaveAttribute(
      'aria-selected',
      'true',
    );
  });

  test('шторка поверх Drawer: Esc закрывает только шторку, фокус на combobox; второй — панель', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    const onClose = vi.fn();
    const { user } = renderWithIntl(
      <Drawer open onClose={onClose} title="Сотрудник">
        <Harness />
      </Drawer>,
    );
    await user.click(combo());
    expect(screen.getByRole('dialog', { name: 'Регион' })).toBeInTheDocument();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog', { name: 'Регион' })).toBeNull();
    expect(onClose).not.toHaveBeenCalled();
    expect(combo()).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
