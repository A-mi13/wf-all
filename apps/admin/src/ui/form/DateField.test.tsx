import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { StrictMode, useState } from 'react';
import { useForm } from 'react-hook-form';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { stubMedia } from '../../test/media';
import { renderWithIntl } from '../../test/render';
import { Button } from '../Button';
import { Drawer } from '../overlay/Drawer';
import { DateField } from './DateField';
import { Form } from './Form';
import { dateFormat } from './isoDate';

// Свой календарь (спека UI-фундамента §4.17): поле текста в формате языка + кнопка календаря, диалог по
// шаблону WAI-ARIA «Date Picker Dialog», неделя — по языку интерфейса, значение — строка YYYY-MM-DD.

function Filters({
  initial = '',
  min,
  max,
  onSubmit = vi.fn(),
}: {
  initial?: string;
  min?: string;
  max?: string;
  onSubmit?: (v: { from: string }) => void;
}) {
  const form = useForm({ defaultValues: { from: initial } });
  return (
    <Form form={form} onSubmit={onSubmit}>
      <DateField name="from" label="С" min={min} max={max} />
      <Button type="submit">Применить</Button>
    </Form>
  );
}

const input = () => screen.getByRole('textbox', { name: 'С' });
const opener = () => screen.getByRole('button', { name: 'С: открыть календарь' });
const dialog = () => screen.getByRole('dialog', { name: 'С: выбор даты' });
const cell = (name: string) => within(dialog()).getByRole('gridcell', { name });

const origTZ = process.env.TZ;
afterEach(() => {
  // незаданный TZ — удалить: присваивание записало бы строку "undefined" (ревью UF2a/II-A, m4)
  if (origTZ === undefined) delete process.env.TZ;
  else process.env.TZ = origTZ;
  vi.restoreAllMocks();
});

describe('DateField: неделя по языку', () => {
  test('ru — первый столбец «пн»', async () => {
    const { user } = renderWithIntl(<Filters initial="2026-10-15" />);
    await user.click(opener());
    const heads = within(dialog()).getAllByRole('columnheader');
    expect(heads.map((h) => h.textContent)).toEqual(['пн', 'вт', 'ср', 'чт', 'пт', 'сб', 'вс']);
    // 1 октября 2026 — четверг: в первой строке три пустых клетки
    const firstRow = within(dialog()).getAllByRole('row')[1]!;
    expect(within(firstRow).getAllByRole('gridcell')[3]).toHaveTextContent('1');
  });

  test('en — первый столбец «Sun»', async () => {
    const { user } = renderWithIntl(<Filters initial="2026-10-15" />, { locale: 'en' });
    await user.click(screen.getByRole('button', { name: 'С: open calendar' }));
    const heads = within(screen.getByRole('dialog')).getAllByRole('columnheader');
    expect(heads[0]).toHaveTextContent('Sun');
    expect(heads[1]).toHaveTextContent('Mon');
  });
});

describe('DateField: значение', () => {
  test('ввод текстом и выбор в сетке дают одну строку YYYY-MM-DD; текст — в формате языка', async () => {
    const onSubmit = vi.fn();
    const { user } = renderWithIntl(<Filters onSubmit={onSubmit} />);
    expect(input()).toHaveAttribute('placeholder', 'ДД.ММ.ГГГГ');
    await user.type(input(), '01.10.2026');
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0]?.[0]).toEqual({ from: '2026-10-01' });

    // календарь открыт на введённом дне; выбор в сетке — та же форма строки
    await user.click(opener());
    expect(cell('1 октября 2026 г.')).toHaveAttribute('aria-selected', 'true');
    await user.click(cell('2 октября 2026 г.'));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(input()).toHaveValue('02.10.2026');
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));
    expect(onSubmit.mock.calls[1]?.[0]).toEqual({ from: '2026-10-02' });
  });

  // Маска по formatToParts (ревью UF2a/II-A, I3): на цифровой клавиатуре iOS (inputMode numeric) нет
  // «.», «/» и пробела — разделители подставляет поле, по порядку частей языка.
  test.each([
    ['ru', '01102026', '01.10.2026'],
    ['en', '10012026', '10/01/2026'],
  ] as const)(
    '%s: 8 цифр подряд — разделители по языку, значение YYYY-MM-DD',
    async (locale, typed, shown) => {
      const onSubmit = vi.fn();
      const { user } = renderWithIntl(<Filters onSubmit={onSubmit} />, { locale });
      const field = screen.getByRole('textbox', { name: 'С' });
      await user.type(field, typed);
      expect(field).toHaveValue(shown);
      await user.click(screen.getByRole('button', { name: /Применить/ }));
      await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
      expect(onSubmit.mock.calls[0]?.[0]).toEqual({ from: '2026-10-01' });
    },
  );

  test('маска: разделитель не дописывается вперёд (Backspace не застревает); свой разделитель и пробел — разделитель языка; лишние цифры отброшены', async () => {
    const { user } = renderWithIntl(<Filters />);
    await user.type(input(), '01');
    expect(input()).toHaveValue('01');
    await user.type(input(), '1');
    expect(input()).toHaveValue('01.1');
    await user.type(input(), '{Backspace}{Backspace}');
    expect(input()).toHaveValue('01');
    await user.clear(input());
    await user.type(input(), '1 1/2026999');
    expect(input()).toHaveValue('1.1.2026');
  });

  // Ревью UF2a/II раунд 1, r1: цифра, вставленная в середину заполненной даты, не перетекает в следующие части —
  // иначе «051.10.2026» стало бы годной, но чужой датой 05.01.1020. Перенос — только у набора подряд (цифры до конца
  // текста) и у вставки сплошных цифр, которых хватает на следующую часть.
  test('маска: цифра в середину заполненной даты — ошибка формата, а не чужая дата', async () => {
    const ru = dateFormat('ru');
    expect(ru.parse(ru.mask('051.10.2026'))).toBeNull();
    expect(ru.parse(ru.mask('01.105.2026'))).toBeNull();
    expect(dateFormat('en').parse(dateFormat('en').mask('10/015/2026'))).toBeNull();
    // Раунд 1 ревью IL2-фронта: вставка любой длины в середину — не только одной цифры. Части после неё уже
    // отделены своими разделителями: цифры не переезжают через них (иначе «0112.10.2026» → 01.12.1020 без ошибки);
    // текст виден как введён, ошибка — у поля.
    for (const text of ['0112.10.2026', '01123.10.2026', '01.1012.2026'])
      expect(ru.parse(ru.mask(text)), text).toBeNull();
    expect(ru.mask('0112.10.2026')).toBe('0112.10.2026');
    expect(dateFormat('en').parse(dateFormat('en').mask('101/01/2026'))).toBeNull();
    // набор подряд и вставка без разделителей (или с частью разделителей) — по-прежнему с переносом
    expect(ru.mask('011')).toBe('01.1');
    expect(ru.mask('01102026')).toBe('01.10.2026');
    expect(ru.mask('0110.2026')).toBe('01.10.2026');
    expect(ru.mask('01.102026')).toBe('01.10.2026');

    const onSubmit = vi.fn();
    const { user } = renderWithIntl(<Filters onSubmit={onSubmit} />);
    await user.type(input(), '01.10.2026');
    await user.type(input(), '5', { initialSelectionStart: 1, initialSelectionEnd: 1 });
    expect(input()).toHaveValue('051.10.2026');
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    expect(input()).toHaveAttribute('aria-invalid', 'true');
    expect(onSubmit).not.toHaveBeenCalled();
  });

  // Раунд 1 ревью IL2-фронта: лишняя цифра года. Набор в конец (курсор в конце, добавлен один знак) — лишнее
  // отбрасывается, как раньше; цифра в середину года или вставка с лишним — ошибка формата, а не молча 2012.
  test('маска: лишняя цифра в середине года — ошибка формата, набор в конец — отброшен', async () => {
    const onSubmit = vi.fn();
    const { user } = renderWithIntl(<Filters onSubmit={onSubmit} />);
    await user.type(input(), '01.10.2026');
    await user.type(input(), '7');
    expect(input()).toHaveValue('01.10.2026');
    await user.type(input(), '1', { initialSelectionStart: 8, initialSelectionEnd: 8 });
    expect(input()).toHaveValue('01.10.20126');
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    await waitFor(() => expect(input()).toHaveAccessibleDescription('Неверный формат'));
    expect(onSubmit).not.toHaveBeenCalled();
    // вставка целиком с лишней цифрой в пустое поле — тоже ошибка, не обрезка
    await user.clear(input());
    await user.paste('01.10.20126');
    expect(input()).toHaveValue('01.10.20126');
    // Раунд 1 ревью хвостов UI, I1: год уже переполнен, затем цифра в конец — отбрасывается только она, прежнее
    // переполнение остаётся ошибкой формата (а не молча 01.10.2012).
    await user.type(input(), '7');
    expect(input()).toHaveValue('01.10.20126');
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    expect(input()).toHaveAccessibleDescription('Неверный формат');
    expect(onSubmit).not.toHaveBeenCalled();
    // функция маски без сведений о наборе лишнее не отбрасывает
    expect(dateFormat('ru').mask('01.10.20126')).toBe('01.10.20126');
    expect(dateFormat('ru').mask('01.10.20267', { typedAtEnd: true })).toBe('01.10.2026');
    expect(dateFormat('ru').mask('01.10.201267', { typedAtEnd: true })).toBe('01.10.20126');
  });

  test('8 цифр подряд разбираются и без маски (вставка в поле)', () => {
    expect(dateFormat('ru').parse('01102026')).toEqual({ y: 2026, m: 10, d: 1 });
    expect(dateFormat('en').parse('10012026')).toEqual({ y: 2026, m: 10, d: 1 });
    expect(dateFormat('ru').parse('0110202')).toBeNull();
  });

  test('год меньше 1000 — ошибка формата, а не 1950 (ревью UF2a/II-A, m6)', async () => {
    const onSubmit = vi.fn();
    const { user } = renderWithIntl(<Filters onSubmit={onSubmit} />);
    await user.type(input(), '01010050');
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    await waitFor(() => expect(input()).toHaveAccessibleDescription('Неверный формат'));
    expect(onSubmit).not.toHaveBeenCalled();
    expect(dateFormat('ru').parse('01.01.0050')).toBeNull();
  });

  test('ISO-строка вставкой тоже понимается и после ухода с поля показывается в формате языка', async () => {
    const onSubmit = vi.fn();
    const { user } = renderWithIntl(<Filters onSubmit={onSubmit} />);
    // вставка из адреса или журнала; набор по символам идёт через маску языка
    await user.click(input());
    await user.paste('2026-10-13');
    await user.tab();
    expect(input()).toHaveValue('13.10.2026');
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0]?.[0]).toEqual({ from: '2026-10-13' });
  });

  test('негодная дата — ошибка формата у поля, отправки нет; пусто — пустая строка', async () => {
    const onSubmit = vi.fn();
    const { user } = renderWithIntl(<Filters onSubmit={onSubmit} />);
    await user.type(input(), '31.02.2026');
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    await waitFor(() => expect(input()).toHaveAccessibleDescription('Неверный формат'));
    expect(onSubmit).not.toHaveBeenCalled();
    await user.clear(input());
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0]?.[0]).toEqual({ from: '' });
  });

  test.each(['America/New_York', 'Asia/Kamchatka'])('пояс %s не сдвигает дату', async (tz) => {
    process.env.TZ = tz;
    const onSubmit = vi.fn();
    const { user } = renderWithIntl(<Filters initial="2026-03-08" onSubmit={onSubmit} />, {
      timeZone: tz,
    });
    expect(input()).toHaveValue('08.03.2026');
    await user.click(opener());
    expect(cell('8 марта 2026 г.')).toHaveAttribute('aria-selected', 'true');
    await user.click(cell('9 марта 2026 г.'));
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(onSubmit.mock.calls[0]?.[0]).toEqual({ from: '2026-03-09' });
  });

  test('max — дни после недоступны (aria-disabled), нажатие не выбирает', async () => {
    const { user } = renderWithIntl(<Filters initial="2026-10-10" max="2026-10-12" />);
    await user.click(opener());
    expect(cell('12 октября 2026 г.')).not.toHaveAttribute('aria-disabled');
    expect(cell('13 октября 2026 г.')).toHaveAttribute('aria-disabled', 'true');
    await user.click(cell('13 октября 2026 г.'));
    expect(dialog()).toBeVisible();
    expect(input()).toHaveValue('10.10.2026');
  });

  test('min — дни до недоступны; вне min текстом — ошибка поля', async () => {
    const onSubmit = vi.fn();
    const { user } = renderWithIntl(<Filters min="2026-10-05" onSubmit={onSubmit} />);
    await user.type(input(), '04.10.2026');
    await user.click(screen.getByRole('button', { name: 'Применить' }));
    await waitFor(() => expect(input()).toHaveAttribute('aria-invalid', 'true'));
    expect(onSubmit).not.toHaveBeenCalled();
    await user.click(opener());
    expect(cell('4 октября 2026 г.')).toHaveAttribute('aria-disabled', 'true');
  });
});

describe('DateField: клавиатура (Date Picker Dialog)', () => {
  test('фокус — на выбранном дне; стрелки, PageUp/PageDown, Shift+PageDown, Home/End; Enter выбирает', async () => {
    const onSubmit = vi.fn();
    const { user } = renderWithIntl(<Filters initial="2026-10-15" onSubmit={onSubmit} />);
    await user.click(opener());
    await waitFor(() => expect(cell('15 октября 2026 г.')).toHaveFocus());
    const title = within(dialog()).getByText(/октябрь 2026/i);
    expect(title).toHaveAttribute('aria-live', 'polite');
    await user.keyboard('{ArrowRight}');
    expect(cell('16 октября 2026 г.')).toHaveFocus();
    await user.keyboard('{ArrowDown}');
    expect(cell('23 октября 2026 г.')).toHaveFocus();
    await user.keyboard('{ArrowLeft}{ArrowUp}');
    expect(cell('15 октября 2026 г.')).toHaveFocus();
    await user.keyboard('{Home}'); // ru — неделя с понедельника
    expect(cell('12 октября 2026 г.')).toHaveFocus();
    await user.keyboard('{End}');
    expect(cell('18 октября 2026 г.')).toHaveFocus();
    await user.keyboard('{PageDown}');
    expect(cell('18 ноября 2026 г.')).toHaveFocus();
    expect(within(dialog()).getByText(/ноябрь 2026/i)).toBeInTheDocument();
    await user.keyboard('{Shift>}{PageDown}{/Shift}');
    expect(cell('18 ноября 2027 г.')).toHaveFocus();
    await user.keyboard('{Shift>}{PageUp}{/Shift}{PageUp}');
    expect(cell('18 октября 2026 г.')).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(opener()).toHaveFocus();
    expect(input()).toHaveValue('18.10.2026');
  });

  // Ревью UF2a/II раунд 1, r3: открыт с первого кадра под StrictMode (история DateField:Open) — фокус на выбранном
  // дне, а не на «‹» (двойной эффект: уборка пузыря вернула фокус на якорь, второй запуск поставил его на первую кнопку).
  test('StrictMode: открыт сразу — фокус на выбранном дне', async () => {
    function Open() {
      const form = useForm({ defaultValues: { from: '2026-10-15' } });
      return (
        <Form form={form} onSubmit={() => undefined}>
          <DateField name="from" label="С" defaultOpen />
        </Form>
      );
    }
    renderWithIntl(
      <StrictMode>
        <Open />
      </StrictMode>,
    );
    await waitFor(() => expect(cell('15 октября 2026 г.')).toHaveFocus());
    // в браузере фокус при показе пузыря ставит Popover по метке (jsdom порядок показа не воспроизводит) —
    // метка только у выбранного дня
    expect(cell('15 октября 2026 г.')).toHaveAttribute('data-autofocus');
    expect(
      within(dialog())
        .getAllByRole('gridcell')
        .filter((c) => c.dataset.autofocus),
    ).toHaveLength(1);
  });

  test('31-е и PageDown — последний день короткого месяца', async () => {
    const { user } = renderWithIntl(<Filters initial="2026-01-31" />);
    await user.click(opener());
    await waitFor(() => expect(cell('31 января 2026 г.')).toHaveFocus());
    await user.keyboard('{PageDown}');
    expect(cell('28 февраля 2026 г.')).toHaveFocus();
  });

  test('на странице: Tab циклится внутри календаря, Esc закрывает, фокус на кнопке', async () => {
    const { user } = renderWithIntl(<Filters initial="2026-10-15" />);
    await user.click(opener());
    const d = dialog();
    for (let i = 0; i < 5; i++) {
      await user.tab();
      expect(d).toContainElement(document.activeElement as HTMLElement);
    }
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(opener()).toHaveFocus();
    expect(input()).toHaveValue('15.10.2026');
  });

  test('в Drawer: Tab циклится в календаре, Esc закрывает только его, второй Esc — панель', async () => {
    function Panel() {
      const [open, setOpen] = useState(true);
      return (
        <Drawer open={open} onClose={() => setOpen(false)} title="Фильтры">
          <Filters initial="2026-10-15" />
        </Drawer>
      );
    }
    const { user } = renderWithIntl(<Panel />);
    await user.click(opener());
    const d = dialog();
    for (let i = 0; i < 5; i++) {
      await user.tab();
      expect(d).toContainElement(document.activeElement as HTMLElement);
    }
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog', { name: 'С: выбор даты' })).toBeNull();
    expect(screen.getByRole('dialog', { name: 'Фильтры' })).toBeInTheDocument();
    expect(opener()).toHaveFocus();
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});

describe('DateField на телефоне', () => {
  test('календарь — шторкой с подписью поля; выбор закрывает, фокус на кнопке', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    const { user } = renderWithIntl(<Filters initial="2026-10-15" />);
    await user.click(opener());
    const sheet = screen.getByRole('dialog', { name: 'С' });
    await user.click(within(sheet).getByRole('gridcell', { name: '20 октября 2026 г.' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(input()).toHaveValue('20.10.2026');
    expect(opener()).toHaveFocus();
  });

  // Safari: касание кнопку не фокусирует — Overlay запомнил открывателем body (ревью UF2a/II-A, I1)
  test('шторка открыта нажатием без фокуса (Safari) — после выбора фокус всё равно на кнопке календаря', () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    renderWithIntl(<Filters initial="2026-10-15" />);
    fireEvent.click(opener());
    expect(opener()).not.toHaveFocus();
    fireEvent.click(screen.getByRole('gridcell', { name: '20 октября 2026 г.' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(input()).toHaveValue('20.10.2026');
    expect(opener()).toHaveFocus();
  });
});

// Как в APG Date Picker Dialog: щелчок вне модального календаря закрывает его (ревью UF2a/II-A, m8).
describe('DateField: щелчок вне календаря', () => {
  test('по пустому месту — закрыт без выбора, фокус на кнопке; внутри — открыт', async () => {
    const { user } = renderWithIntl(
      <>
        <Filters initial="2026-10-15" />
        <p>Снаружи</p>
      </>,
    );
    await user.click(opener());
    await user.click(within(dialog()).getByRole('button', { name: 'Следующий месяц' }));
    expect(dialog()).toBeVisible();
    await user.click(screen.getByText('Снаружи'));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(opener()).toHaveFocus();
    expect(input()).toHaveValue('15.10.2026');
  });

  test('по другому полю — закрыт, фокус остаётся в том поле', async () => {
    const { user } = renderWithIntl(<Filters initial="2026-10-15" />);
    await user.click(opener());
    await user.click(input());
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(input()).toHaveFocus();
  });

  test('щелчок по кнопке календаря при открытом — закрывает (переключатель), фокус на кнопке', async () => {
    const { user } = renderWithIntl(<Filters initial="2026-10-15" />);
    await user.click(opener());
    await user.click(opener());
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(opener()).toHaveFocus();
  });
});
