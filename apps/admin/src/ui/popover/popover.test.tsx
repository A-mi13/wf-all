import { act, createEvent, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { type ReactNode, StrictMode, useRef, useState } from 'react';
import { afterEach, describe, expect, onTestFinished, test, vi } from 'vitest';
import { renderWithIntl } from '../../test/render';
import { Drawer } from '../overlay/Drawer';
import { isOverlayStackEmpty } from '../overlay/stack';
import { GAP, MARGIN } from './position';
import { Popover, type PopoverProps } from './Popover';

// Popover (спека UI-фундамента §4.8): верхний слой, положение от СВОЕГО якоря числами на самом пузыре,
// немодальный закрывается, когда фокус покинул и якорь, и пузырь; нажатие внутри пузыря не забирает
// фокус у вызывателя (кроме полей ввода); якорь пропал — закрыть.

// Прямоугольник элемента — из data-rect="left,top,width,height" (jsdom не раскладывает).
function stubRects() {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (
    this: HTMLElement,
  ) {
    const [left = 0, top = 0, width = 0, height = 0] = (this.dataset.rect ?? '')
      .split(',')
      .map(Number);
    return {
      left,
      top,
      width,
      height,
      right: left + width,
      bottom: top + height,
      x: left,
      y: top,
      toJSON: () => ({}),
    } as DOMRect;
  });
}

afterEach(() => {
  vi.restoreAllMocks();
});

type HarnessProps = {
  kind?: PopoverProps['kind'];
  initiallyOpen?: boolean;
  rect?: string;
  onClose?: PopoverProps['onClose'];
  children?: ReactNode;
  name?: string;
};

function Harness({
  kind = 'menu',
  initiallyOpen = false,
  rect,
  onClose,
  children,
  name = 'Роль',
}: HarnessProps) {
  const anchor = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(initiallyOpen);
  return (
    <>
      <button ref={anchor} type="button" data-rect={rect} onClick={() => setOpen((v) => !v)}>
        {name}
      </button>
      <Popover
        anchor={anchor}
        open={open}
        kind={kind}
        label={`Пузырь ${name}`}
        onClose={(reason) => {
          onClose?.(reason);
          setOpen(false);
        }}
      >
        {children ?? (
          <div role="listbox" aria-label={`Варианты ${name}`}>
            <div role="option" aria-selected="false">
              Модератор
            </div>
          </div>
        )}
      </Popover>
    </>
  );
}

const bubbleOf = (el: HTMLElement) => el.closest('[popover]') as HTMLElement;

describe('Popover', () => {
  test('закрытый — нет в дереве доступности; открытый — виден (страж прокладки jsdom)', async () => {
    const user = userEvent.setup();
    render(<Harness />);
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(document.querySelector('[popover]')).not.toBeNull();
    await user.click(screen.getByRole('button', { name: 'Роль' }));
    expect(screen.getByRole('listbox', { name: 'Варианты Роль' })).toBeVisible();
    expect(bubbleOf(screen.getByRole('listbox')).matches(':popover-open')).toBe(true);
    // закрыли снова — опять нет в дереве (состояние прокладки снято, а не только поставлено)
    await user.click(screen.getByRole('button', { name: 'Роль' }));
    expect(screen.queryByRole('listbox')).toBeNull();
    expect(document.querySelector('[popover]')?.matches(':popover-open')).toBe(false);
  });

  test('два открытых пузыря у разных якорей — у каждого свои --wf-pop-* от своего якоря (N3)', () => {
    stubRects();
    render(
      <>
        <Harness kind="hint" name="Первая" rect="100,100,80,20" initiallyOpen />
        <Harness kind="hint" name="Средняя" rect="100,400,80,20" initiallyOpen />
      </>,
    );
    const [first, middle] = screen.getAllByRole('tooltip');
    expect(first!.style.getPropertyValue('--wf-pop-y')).toBe(`${120 + GAP}px`);
    expect(middle!.style.getPropertyValue('--wf-pop-y')).toBe(`${420 + GAP}px`);
    expect(first!.style.getPropertyValue('--wf-pop-x')).toBe('100px');
    expect(first!.style.getPropertyValue('--wf-pop-max-h')).not.toBe('');
  });

  test('mousedown в пузыре отменён (фокус остаётся на якоре); на input — нет; pointerdown — не отменён', async () => {
    const user = userEvent.setup();
    render(
      <Harness>
        <div role="listbox" aria-label="Варианты">
          <div role="option" aria-selected="false">
            Модератор
          </div>
        </div>
        <input aria-label="Поиск в пузыре" />
      </Harness>,
    );
    const anchor = screen.getByRole('button', { name: 'Роль' });
    await user.click(anchor);
    const option = screen.getByRole('option', { name: 'Модератор' });
    const down = createEvent.mouseDown(option);
    fireEvent(option, down);
    expect(down.defaultPrevented).toBe(true);
    expect(anchor).toHaveFocus();
    const pointer = createEvent.pointerDown(option);
    fireEvent(option, pointer);
    expect(pointer.defaultPrevented).toBe(false);
    const input = screen.getByLabelText('Поиск в пузыре');
    const onInput = createEvent.mouseDown(input);
    fireEvent(input, onInput);
    expect(onInput.defaultPrevented).toBe(false);
  });

  // Ревью UF2a/I, m4: в Safari щелчок по кнопке фокус ей не даёт — focusout не случается, пузырь
  // закрывается нажатием вне якоря и пузыря.
  test('pointerdown вне якоря и пузыря закрывает немодальный; на якоре и в пузыре — нет; модальный — нет', async () => {
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(
      <>
        <Harness onClose={onClose} />
        <p>Снаружи</p>
      </>,
    );
    const anchor = screen.getByRole('button', { name: 'Роль' });
    await user.click(anchor);
    fireEvent.pointerDown(screen.getByRole('option', { name: 'Модератор' }));
    fireEvent.pointerDown(anchor);
    expect(onClose).not.toHaveBeenCalled();
    const outside = createEvent.pointerDown(screen.getByText('Снаружи'));
    fireEvent(screen.getByText('Снаружи'), outside);
    expect(outside.defaultPrevented).toBe(false);
    expect(onClose).toHaveBeenCalledWith('outside');
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  // Как APG Date Picker Dialog (ревью UF2a/II-A, m8): щелчок вне модального пузыря закрывает его — по
  // click, когда фокус уже ушёл туда, куда щёлкнули: по пустому месту — фокус на якорь, по полю — в поле.
  test('модальный пузырь: щелчок снаружи закрывает (outside), фокус на якорь; щелчок внутри — нет', async () => {
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(
      <>
        <Harness kind="modal" onClose={onClose}>
          <button type="button">День</button>
        </Harness>
        <p>Снаружи</p>
      </>,
    );
    await user.click(screen.getByRole('button', { name: 'Роль' }));
    await user.click(screen.getByRole('button', { name: 'День' }));
    fireEvent.pointerDown(screen.getByText('Снаружи'));
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole('dialog')).toBeVisible();
    await user.click(screen.getByText('Снаружи'));
    expect(onClose).toHaveBeenCalledWith('outside');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.getByRole('button', { name: 'Роль' })).toHaveFocus();
  });

  test('focusout якоря на нефокусируемое (relatedTarget = null) — закрыт', async () => {
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(<Harness onClose={onClose} />);
    const anchor = screen.getByRole('button', { name: 'Роль' });
    await user.click(anchor);
    fireEvent.focusOut(anchor, { relatedTarget: null });
    expect(onClose).toHaveBeenCalledWith('focusout');
    expect(screen.queryByRole('listbox')).toBeNull();
  });

  test('фокус уходит внутрь пузыря (меню с флажками) — открыт; из пузыря наружу — закрыт', async () => {
    const onClose = vi.fn();
    const user = userEvent.setup();
    render(
      <>
        <Harness onClose={onClose}>
          <button type="button">Флажок</button>
        </Harness>
        <button type="button">Снаружи</button>
      </>,
    );
    const anchor = screen.getByRole('button', { name: 'Роль' });
    await user.click(anchor);
    const inside = screen.getByRole('button', { name: 'Флажок' });
    fireEvent.focusOut(anchor, { relatedTarget: inside });
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.focusOut(inside, { relatedTarget: screen.getByRole('button', { name: 'Снаружи' }) });
    expect(onClose).toHaveBeenCalledWith('focusout');
  });

  test('якорь удалён из DOM — onClose', async () => {
    const onClose = vi.fn();
    function Removable() {
      const anchor = useRef<HTMLButtonElement>(null);
      const [shown, setShown] = useState(true);
      return (
        <>
          {shown && (
            <button ref={anchor} type="button">
              Якорь
            </button>
          )}
          <button type="button" onClick={() => setShown(false)}>
            Убрать
          </button>
          <Popover anchor={anchor} open kind="menu" onClose={onClose}>
            <p>Содержимое</p>
          </Popover>
        </>
      );
    }
    render(<Removable />);
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Убрать' }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  test('прокрутка предка: якорь ушёл из видимой области — подсказка закрыта, меню прижато к краю', async () => {
    stubRects();
    const hint = vi.fn();
    const menu = vi.fn();
    render(
      <div data-testid="scroller">
        <Harness kind="hint" name="Подсказка" rect="100,100,80,20" initiallyOpen onClose={hint} />
        <Harness kind="menu" name="Меню" rect="300,100,80,20" initiallyOpen onClose={menu} />
      </div>,
    );
    // оба якоря ушли за верх окна
    screen.getByRole('button', { name: 'Подсказка' }).dataset.rect = '100,-200,80,20';
    screen.getByRole('button', { name: 'Меню' }).dataset.rect = '300,-200,80,20';
    fireEvent.scroll(screen.getByTestId('scroller'));
    await waitFor(() => expect(hint).toHaveBeenCalledWith('scroll'));
    expect(menu).not.toHaveBeenCalled();
    const bubble = bubbleOf(screen.getByRole('listbox', { name: 'Варианты Меню' }));
    await waitFor(() => expect(bubble.style.getPropertyValue('--wf-pop-y')).toBe(`${MARGIN}px`));
  });

  // Ревью UF2a/I, I1: содержимое пузыря меняет размер после открытия (опции списка пришли с сервера) —
  // положение пересчитывается: ResizeObserver наблюдает и сам пузырь. Иначе перевёрнутый наверх пузырь
  // рос бы вниз от старого y и накрывал свой якорь.
  test('пузырь вырос у нижнего края — пересчёт: перевёрнут наверх и не накрывает якорь', async () => {
    stubRects();
    // упади ожидание раньше конца — подмена не переживёт тест (ревью UF2a/I, n2)
    onTestFinished(() => {
      vi.unstubAllGlobals();
    });
    const observed: Element[] = [];
    const callbacks: (() => void)[] = [];
    vi.stubGlobal(
      'ResizeObserver',
      class {
        constructor(cb: () => void) {
          callbacks.push(cb);
        }
        observe(el: Element) {
          observed.push(el);
        }
        unobserve() {}
        disconnect() {}
      },
    );
    vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(function (
      this: HTMLElement,
    ) {
      return Number(this.dataset.h ?? 0);
    });
    // якорь у нижнего края окна jsdom (1024 × 768): снизу 36 px, сверху 688
    render(<Harness kind="menu" rect="100,700,80,20" initiallyOpen />);
    const bubble = bubbleOf(screen.getByRole('listbox'));
    expect(observed).toContain(bubble);
    expect(bubble.style.getPropertyValue('--wf-pop-y')).toBe(`${720 + GAP}px`);
    // пришли опции: высота 300 — снизу не помещается
    bubble.dataset.h = '300';
    act(() => callbacks.forEach((cb) => cb()));
    await waitFor(() =>
      expect(bubble.style.getPropertyValue('--wf-pop-y')).toBe(`${700 - GAP - 300}px`),
    );
    expect(bubble.dataset.side).toBe('top');
  });

  test('закрытие снимает слушатели прокрутки, размера и клавиш', async () => {
    const add = vi.spyOn(document, 'addEventListener');
    const remove = vi.spyOn(document, 'removeEventListener');
    const winAdd = vi.spyOn(window, 'addEventListener');
    const winRemove = vi.spyOn(window, 'removeEventListener');
    const user = userEvent.setup();
    render(<Harness />);
    const anchor = screen.getByRole('button', { name: 'Роль' });
    await user.click(anchor);
    await user.click(anchor);
    const count = (spy: typeof add, type: string) =>
      spy.mock.calls.filter(([t]) => t === type).length;
    for (const type of ['scroll', 'keydown'])
      expect(count(remove, type), type).toBe(count(add, type));
    expect(count(add, 'scroll')).toBeGreaterThan(0);
    expect(count(winRemove, 'resize')).toBe(count(winAdd, 'resize'));
  });

  test('StrictMode: двойной эффект не бросает InvalidStateError; закрыт — запись снята', () => {
    const { rerender } = render(
      <StrictMode>
        <Harness kind="modal" initiallyOpen />
      </StrictMode>,
    );
    expect(screen.getByRole('dialog', { name: 'Пузырь Роль' })).toBeVisible();
    expect(screen.getByRole('dialog', { name: 'Пузырь Роль' })).toHaveAttribute(
      'aria-modal',
      'true',
    );
    rerender(<StrictMode>{null}</StrictMode>);
    expect(isOverlayStackEmpty()).toBe(true);
    expect(document.body).not.toHaveAttribute('data-wf-scroll-lock');
  });

  test('Popover внутри Drawer — потомок его role="dialog" (рендер на месте, без портала)', async () => {
    const { user } = renderWithIntl(
      <Drawer open onClose={() => {}} title="Сотрудник">
        <Harness />
      </Drawer>,
    );
    await user.click(screen.getByRole('button', { name: 'Роль' }));
    expect(screen.getByRole('dialog', { name: 'Сотрудник' })).toContainElement(
      screen.getByRole('listbox'),
    );
    act(() => {});
  });

  // Календарь DateField рендерит сетку только открытым: закрытие снимает день с фокусом тем же рендером,
  // фокус уходит на body раньше уборки Popover — модальный всё равно возвращает его на якорь.
  test('модальный: содержимое снято вместе с закрытием — фокус на якорь, не на body', async () => {
    function Lazy() {
      const anchor = useRef<HTMLButtonElement>(null);
      const [open, setOpen] = useState(false);
      return (
        <>
          <button ref={anchor} type="button" onClick={() => setOpen(true)}>
            Календарь
          </button>
          <Popover
            anchor={anchor}
            open={open}
            kind="modal"
            label="Дата"
            onClose={() => setOpen(false)}
          >
            {open && (
              <button type="button" onClick={() => setOpen(false)}>
                15
              </button>
            )}
          </Popover>
        </>
      );
    }
    const user = userEvent.setup();
    render(<Lazy />);
    await user.click(screen.getByRole('button', { name: 'Календарь' }));
    expect(screen.getByRole('button', { name: '15' })).toHaveFocus();
    await user.keyboard('{Enter}');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.getByRole('button', { name: 'Календарь' })).toHaveFocus();
  });

  test('kind: роли — hint → tooltip, modal → dialog + aria-modal, menu — роль у содержимого', () => {
    render(
      <>
        <Harness kind="hint" name="А" initiallyOpen>
          подсказка
        </Harness>
        <Harness kind="menu" name="Б" initiallyOpen />
      </>,
    );
    expect(screen.getByRole('tooltip')).toHaveTextContent('подсказка');
    expect(bubbleOf(screen.getByRole('listbox')).getAttribute('role')).toBeNull();
  });
});
