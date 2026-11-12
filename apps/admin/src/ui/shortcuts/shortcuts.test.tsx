import { act, fireEvent, screen, within } from '@testing-library/react';
import { type ReactNode, StrictMode, useEffect, useState } from 'react';
import { afterEach, describe, expect, test, vi } from 'vitest';
import { stubMedia } from '../../test/media';
import { renderWithIntl } from '../../test/render';
import { Modal } from '../overlay';
import { pushOverlay } from '../overlay/stack';
import { SHORTCUTS_SINGLE_KEY } from './prefs';
import { ShortcutsProvider } from './ShortcutsProvider';
import { type ShortcutOptions, useOpenShortcutsHelp, useShortcut } from './useShortcut';

// Горячие клавиши (спека UI-фундамента §4.2, план UF4.6) — на тестовых регистрациях: g x из commands.ts и
// Ctrl+K палитры регистрирует волна 2.

function Probe(props: { combo: string; run: () => void } & ShortcutOptions) {
  const { combo, run, ...opts } = props;
  useShortcut(combo, run, opts);
  return null;
}

function setup(children: ReactNode) {
  return renderWithIntl(
    <StrictMode>
      <ShortcutsProvider>{children}</ShortcutsProvider>
    </StrictMode>,
  );
}

// Нажатие по физической клавише: code — клавиша, key — символ раскладки.
function press(code: string, key: string, init: KeyboardEventInit = {}) {
  fireEvent.keyDown(document.activeElement ?? document.body, { code, key, ...init });
}
const ctrlK = () => press('KeyK', 'k', { ctrlKey: true });
const openHelp = () => press('Slash', '?', { shiftKey: true });

afterEach(() => {
  vi.useRealTimers();
  localStorage.clear();
});

describe('useShortcut', () => {
  test('g c в русской раскладке — «Города»', () => {
    const cities = vi.fn();
    setup(<Probe combo="g c" run={cities} label="Города" />);
    press('KeyG', 'п');
    press('KeyC', 'с');
    expect(cities).toHaveBeenCalledTimes(1);
  });

  test('g и пауза 1,1 с, затем c — ничего', () => {
    vi.useFakeTimers();
    const cities = vi.fn();
    setup(<Probe combo="g c" run={cities} label="Города" />);
    press('KeyG', 'g');
    act(() => vi.advanceTimersByTime(900));
    press('KeyC', 'c');
    expect(cities).toHaveBeenCalledTimes(1); // в пределах секунды — срабатывает
    press('KeyG', 'g');
    act(() => vi.advanceTimersByTime(1100));
    press('KeyC', 'c');
    expect(cities).toHaveBeenCalledTimes(1);
  });

  test('в поле ввода / не срабатывает, Ctrl+K с allowInInput — срабатывает', () => {
    const search = vi.fn();
    const palette = vi.fn();
    setup(
      <>
        <Probe combo="/" run={search} label="Поиск" />
        <Probe combo="mod+k" run={palette} label="Палитра" allowInInput />
        <input aria-label="Поле" />
        <div role="combobox" tabIndex={0} aria-label="Список" aria-expanded={false} />
      </>,
    );
    screen.getByRole('textbox', { name: 'Поле' }).focus();
    press('Slash', '/');
    screen.getByRole('combobox', { name: 'Список' }).focus();
    press('Slash', '/');
    expect(search).not.toHaveBeenCalled();
    ctrlK();
    expect(palette).toHaveBeenCalledTimes(1);
    (document.activeElement as HTMLElement).blur();
    press('Slash', '/');
    expect(search).toHaveBeenCalledTimes(1);
  });

  test('ввод IME и зажатые Ctrl/Alt/Meta — не срабатывает', () => {
    const cities = vi.fn();
    setup(<Probe combo="g c" run={cities} label="Города" />);
    press('KeyG', 'g', { isComposing: true });
    press('KeyC', 'c');
    press('KeyG', 'g', { ctrlKey: true });
    press('KeyC', 'c');
    press('KeyG', 'g');
    press('KeyC', 'c', { altKey: true });
    press('KeyG', 'g', { metaKey: true });
    press('KeyC', 'c');
    expect(cities).not.toHaveBeenCalled();
    // клавиша-модификатор между шагами последовательность не рвёт
    press('KeyG', 'g');
    press('ShiftLeft', 'Shift', { shiftKey: true });
    press('KeyC', 'c');
    expect(cities).toHaveBeenCalledTimes(1);
  });

  test('под модалкой Ctrl+K не открывает палитру', () => {
    const palette = vi.fn();
    const cities = vi.fn();
    setup(
      <>
        <Probe combo="mod+k" run={palette} label="Палитра" allowInInput />
        <Probe combo="g c" run={cities} label="Города" />
        <Modal open onClose={() => {}} title="Окно" />
      </>,
    );
    ctrlK();
    press('KeyG', 'g');
    press('KeyC', 'c');
    expect(palette).not.toHaveBeenCalled();
    expect(cities).not.toHaveBeenCalled();
  });

  test('подсказка открыта → Ctrl+K открывает палитру', () => {
    const palette = vi.fn();
    setup(<Probe combo="mod+k" run={palette} label="Палитра" allowInInput />);
    const pop = pushOverlay(Symbol('hint'), null, { modal: false });
    try {
      ctrlK();
    } finally {
      pop();
    }
    expect(palette).toHaveBeenCalledTimes(1);
  });

  test('переключатель выключил односимвольные — g c молчит, Ctrl+K работает', async () => {
    const cities = vi.fn();
    const palette = vi.fn();
    const { user } = setup(
      <>
        <Probe combo="g c" run={cities} label="Города" />
        <Probe combo="mod+k" run={palette} label="Палитра" allowInInput />
      </>,
    );
    openHelp();
    const dialog = await screen.findByRole('dialog', { name: 'Горячие клавиши' });
    const single = within(dialog).getByRole('switch', { name: 'Односимвольные клавиши' });
    expect(single).toHaveAttribute('aria-checked', 'true');
    await user.click(single);
    expect(single).toHaveAttribute('aria-checked', 'false');
    expect(localStorage.getItem(SHORTCUTS_SINGLE_KEY)).toBe('0');
    await user.click(within(dialog).getByRole('button', { name: 'Закрыть' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    press('KeyG', 'g');
    press('KeyC', 'c');
    openHelp();
    expect(cities).not.toHaveBeenCalled();
    expect(screen.queryByRole('dialog')).toBeNull(); // ? — тоже односимвольная
    ctrlK();
    expect(palette).toHaveBeenCalledTimes(1);
  });

  test('предпочтение из storage: выключено — молчит; storage бросает — включены', () => {
    localStorage.setItem(SHORTCUTS_SINGLE_KEY, '0');
    const cities = vi.fn();
    const view = setup(<Probe combo="g c" run={cities} label="Города" />);
    press('KeyG', 'g');
    press('KeyC', 'c');
    expect(cities).not.toHaveBeenCalled();
    view.unmount();
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('private');
    });
    try {
      setup(<Probe combo="g c" run={cities} label="Города" />);
      press('KeyG', 'g');
      press('KeyC', 'c');
      expect(cities).toHaveBeenCalledTimes(1);
    } finally {
      spy.mockRestore();
    }
  });

  test('конфликт регистраций — ошибка; одна регистрация в StrictMode — без ошибки', () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    try {
      const view = setup(<Probe combo="mod+k" run={() => {}} label="Палитра" />);
      expect(error).not.toHaveBeenCalled();
      view.unmount();
      setup(
        <>
          <Probe combo="mod+k" run={() => {}} label="Палитра" />
          <Probe combo="Mod+K" run={() => {}} label="Другое" />
        </>,
      );
      expect(error).toHaveBeenCalledWith(expect.stringContaining('Mod+K'));
    } finally {
      error.mockRestore();
    }
  });

  test('шпаргалка содержит все зарегистрированные и не держит снятые', async () => {
    // снятие регистрации без рендера провайдера (раздел ушёл сам) — строка пропадает по подписке на реестр
    const palette = { hide: () => {} };
    function Removable() {
      const [on, setOn] = useState(true);
      useEffect(() => {
        palette.hide = () => setOn(false);
      }, []);
      return on ? <Probe combo="mod+k" run={() => {}} label="Палитра" /> : null;
    }
    setup(
      <>
        <Probe combo="g c" run={() => {}} label="Города" />
        <Removable />
        <Probe combo="/" run={() => {}} label="Поиск" />
        <Probe combo="g o" run={() => {}} label="Страны" enabled={false} />
      </>,
    );
    openHelp();
    const dialog = await screen.findByRole('dialog', { name: 'Горячие клавиши' });
    const row = (name: string) => within(dialog).getByText(name).closest('li');
    expect(row('Города')).toHaveTextContent(/g\s*затем\s*c/);
    expect(row('Палитра')).toHaveTextContent(/Ctrl\s*\+\s*K/);
    expect(row('Поиск')).toHaveTextContent('/');
    expect(row('Шпаргалка по клавишам')).toHaveTextContent('?');
    expect(within(dialog).queryByText('Страны')).toBeNull();
    act(() => palette.hide());
    expect(within(dialog).queryByText('Палитра')).toBeNull();
    expect(within(dialog).getByText('Города')).toBeInTheDocument();
  });

  test('шпаргалка: символ по раскладке (getLayoutMap), без него — латиница', async () => {
    Object.defineProperty(navigator, 'keyboard', {
      configurable: true,
      value: {
        getLayoutMap: () =>
          Promise.resolve(
            new Map([
              ['KeyG', 'п'],
              ['KeyC', 'с'],
              ['Slash', '.'],
            ]),
          ),
      },
    });
    try {
      setup(<Probe combo="g c" run={() => {}} label="Города" />);
      openHelp();
      const dialog = await screen.findByRole('dialog', { name: 'Горячие клавиши' });
      const row = () => within(dialog).getByText('Города').closest('li');
      expect(await within(dialog).findByText('п')).toBeInTheDocument();
      expect(row()).toHaveTextContent(/п\s*затем\s*с/);
      // ? в русской раскладке — Shift и клавиша «.» (по code Slash)
      expect(within(dialog).getByText('Шпаргалка по клавишам').closest('li')).toHaveTextContent(
        /Shift\s*\+\s*\./,
      );
    } finally {
      Reflect.deleteProperty(navigator, 'keyboard');
    }
  });

  test('карманное устройство — без действий «только с компьютера»', async () => {
    stubMedia({ width: 390, pointer: 'coarse' });
    const grant = vi.fn();
    const cities = vi.fn();
    setup(
      <>
        <Probe combo="g r" run={grant} label="Выдать роль" permission="staff.roles.grant" />
        <Probe combo="g c" run={cities} label="Города" />
      </>,
    );
    press('KeyG', 'g');
    press('KeyR', 'r');
    press('KeyG', 'g');
    press('KeyC', 'c');
    expect(grant).not.toHaveBeenCalled();
    expect(cities).toHaveBeenCalledTimes(1);
    openHelp();
    const dialog = await screen.findByRole('dialog', { name: 'Горячие клавиши' });
    expect(within(dialog).queryByText('Выдать роль')).toBeNull();
    expect(within(dialog).getByText('Города')).toBeInTheDocument();
  });

  test('на компьютере действие «только с компьютера» работает', () => {
    const grant = vi.fn();
    setup(<Probe combo="g r" run={grant} label="Выдать роль" permission="staff.roles.grant" />);
    press('KeyG', 'g');
    press('KeyR', 'r');
    expect(grant).toHaveBeenCalledTimes(1);
  });

  test('useOpenShortcutsHelp открывает шпаргалку (команда палитры)', async () => {
    function Opener() {
      const open = useOpenShortcutsHelp();
      return (
        <button type="button" onClick={open}>
          Шпаргалка
        </button>
      );
    }
    const { user } = setup(<Opener />);
    await user.click(screen.getByRole('button', { name: 'Шпаргалка' }));
    expect(await screen.findByRole('dialog', { name: 'Горячие клавиши' })).toBeInTheDocument();
  });

  test('без провайдера — понятная ошибка', () => {
    const error = vi.spyOn(console, 'error').mockImplementation(() => {});
    try {
      expect(() => renderWithIntl(<Probe combo="g c" run={() => {}} />)).toThrow(
        /ShortcutsProvider/,
      );
    } finally {
      error.mockRestore();
    }
  });
});
