import {
  type MouseEvent,
  type ReactNode,
  type RefObject,
  useEffectEvent,
  useLayoutEffect,
  useRef,
} from 'react';
import { setCssPx } from '../cssVar';
import { cx } from '../cx';
import {
  focusables,
  isTopModal,
  isTopOverlay,
  onModalOpen,
  pushOverlay,
  trapTab,
} from '../overlay/stack';
import styles from './Popover.module.css';
import { type AnchorRect, type Placement, place } from './position';

export type PopoverKind = 'hint' | 'menu' | 'modal';
export type PopoverCloseReason = 'escape' | 'focusout' | 'overlay' | 'scroll';

export type PopoverProps = {
  anchor: RefObject<HTMLElement | null>;
  open: boolean;
  onClose: (reason: PopoverCloseReason) => void;
  placement?: Placement;
  kind: PopoverKind;
  label?: string; // имя диалога (kind 'modal')
  id?: string; // для aria-controls / aria-describedby вызывателя
  className?: string; // оформление содержимого (подсказка, меню) — у вызывателя
  children: ReactNode;
};

const ROLE: Record<PopoverKind, string | undefined> = {
  hint: 'tooltip',
  menu: undefined, // роль задаёт содержимое (listbox, menu)
  modal: 'dialog',
};

// Основа всего всплывающего (спека UI-фундамента §4.8): элемент popover="manual" в верхнем слое —
// overflow предков (тело таблицы, тело панели) его не режет. Рендер на месте, без портала (m14): пузырь
// внутри aria-modal-панели, aria-activedescendant вызывателя ссылается внутрь диалога.
// - Стек оверлеев (stack.ts): hint и menu — немодальная запись (только Esc верхней из всех), modal —
//   модальная (своя ловушка Tab, блок прокрутки). Открытие любого Overlay закрывает пузырь ('overlay').
// - Немодальный закрывается по focusout, когда фокус покинул И якорь, И пузырь (relatedTarget null —
//   щелчок по нефокусируемому — тоже уход): слушатель Overlay гасит Tab раньше нас, keydown с
//   defaultPrevented не годится (m-j). mousedown внутри немодального пузыря отменён (фокус остаётся на
//   вызывателе, выбор мышью не теряется, R4-1), кроме полей ввода; pointerdown не отменяется (m-n).
// - Положение — JS от СВОЕГО якоря числами --wf-pop-x/-y/-max-h на самом пузыре (setCssPx, N3): при
//   открытии, при прокрутке любого предка (scroll с capture), resize и ResizeObserver якоря и пузыря — один кадр
//   на пересчёт. Якорь ушёл из видимой области — подсказка закрывается ('scroll'), меню прижато к краю.
//   Якорь пропал из документа или подменён — закрыть ('focusout').
// - Модальный (календарь): фокус внутрь при открытии, обратно на якорь при закрытии.
export function Popover({
  anchor,
  open,
  onClose,
  placement = 'bottom-start',
  kind,
  label,
  id,
  className,
  children,
}: PopoverProps) {
  const ref = useRef<HTMLDivElement>(null);
  const close = useEffectEvent((reason: PopoverCloseReason) => onClose(reason));
  const modal = kind === 'modal';

  useLayoutEffect(() => {
    const el = ref.current;
    const target = anchor.current;
    if (!open || !el) return;
    let closed = false;
    const closeOnce = (reason: PopoverCloseReason) => {
      if (closed) return;
      closed = true;
      close(reason);
    };

    // Показ: на уже открытом или отключённом элементе браузер бросает InvalidStateError.
    if (el.isConnected && !el.matches(':popover-open')) {
      try {
        el.showPopover();
      } catch {
        // элемент вне документа или уже показан — положение и стек всё равно ведём
      }
    }

    const stackId = Symbol('popover');
    const release = pushOverlay(stackId, modal ? el : null, { modal });
    // подписка — после своей записи: своё открытие себя не закрывает
    const offModal = onModalOpen(() => closeOnce('overlay'));

    const update = () => {
      const a = anchor.current;
      if (!a || a !== target || !a.isConnected) return closeOnce('focusout');
      const rect = a.getBoundingClientRect();
      const viewport = {
        width: document.documentElement.clientWidth || window.innerWidth,
        height: document.documentElement.clientHeight || window.innerHeight,
      };
      if (kind === 'hint' && !inView(a, rect, viewport)) return closeOnce('scroll');
      const p = place(
        rect,
        { width: el.offsetWidth, height: el.scrollHeight },
        viewport,
        placement,
      );
      setCssPx(el, '--wf-pop-x', p.x);
      setCssPx(el, '--wf-pop-y', p.y);
      setCssPx(el, '--wf-pop-max-h', p.maxHeight);
      el.dataset.side = p.side;
    };
    let frame = 0;
    const schedule = () => {
      if (frame) return;
      frame = requestFrame(() => {
        frame = 0;
        update();
      });
    };
    update();

    document.addEventListener('scroll', schedule, { capture: true, passive: true });
    window.addEventListener('resize', schedule);
    const resize =
      typeof ResizeObserver === 'function' && target ? new ResizeObserver(schedule) : null;
    if (target) resize?.observe(target);
    // и сам пузырь: содержимое выросло или ужалось после открытия (опции пришли с сервера) — сторона и
    // y пересчитываются, перевёрнутый наверх пузырь не растёт вниз на свой якорь (ревью UF2a/I, I1)
    resize?.observe(el);
    // якорь размонтирован или подменён без прокрутки — заметить по изменению дерева
    const tree = new MutationObserver(() => {
      if (!target?.isConnected || anchor.current !== target) closeOnce('focusout');
    });
    tree.observe(document.body, { childList: true, subtree: true });

    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented) return;
      if (e.key === 'Escape' && isTopOverlay(stackId)) {
        e.preventDefault();
        closeOnce('escape');
      } else if (modal && e.key === 'Tab' && isTopModal(stackId)) {
        trapTab(e, el);
      }
    };
    document.addEventListener('keydown', onKey);

    // немодальный: фокус покинул и якорь, и пузырь
    const onFocusOut = (e: FocusEvent) => {
      const next = e.relatedTarget instanceof Node ? e.relatedTarget : null;
      if (next && (target?.contains(next) || el.contains(next))) return;
      closeOnce('focusout');
    };
    if (!modal) {
      target?.addEventListener('focusout', onFocusOut);
      el.addEventListener('focusout', onFocusOut);
    } else if (!el.contains(document.activeElement)) {
      (focusables(el)[0] ?? el).focus();
    }

    return () => {
      closed = true;
      if (frame) cancelFrame(frame);
      document.removeEventListener('scroll', schedule, { capture: true });
      window.removeEventListener('resize', schedule);
      resize?.disconnect();
      tree.disconnect();
      document.removeEventListener('keydown', onKey);
      target?.removeEventListener('focusout', onFocusOut);
      el.removeEventListener('focusout', onFocusOut);
      offModal();
      release();
      // модальный вернул фокус на якорь — до скрытия (скрытый пузырь фокус теряет)
      if (modal && el.contains(document.activeElement) && target?.isConnected) target.focus();
      if (el.isConnected && el.matches(':popover-open')) {
        try {
          el.hidePopover();
        } catch {
          // уже скрыт браузером (элемент удалён из документа)
        }
      }
    };
  }, [open, anchor, kind, modal, placement]);

  // Нажатие внутри немодального пузыря не забирает фокус у вызывателя; полям ввода фокус нужен.
  const onMouseDown = (e: MouseEvent<HTMLDivElement>) => {
    if (modal) return;
    if (e.target instanceof Element && e.target.closest('input, textarea')) return;
    e.preventDefault();
  };

  return (
    <div
      ref={ref}
      id={id}
      popover="manual"
      role={ROLE[kind]}
      aria-modal={modal ? 'true' : undefined}
      aria-label={modal ? label : undefined}
      tabIndex={modal ? -1 : undefined}
      className={cx(styles.popover, className)}
      onMouseDown={onMouseDown}
    >
      {children}
    </div>
  );
}

// Якорь виден: пересекается с окном и с каждым прокручиваемым предком (overflow не visible). Касание
// края считается видимым (строгие сравнения): якорь нулевого размера в углу не закрывает подсказку.
function inView(
  anchor: HTMLElement,
  rect: AnchorRect,
  viewport: { width: number; height: number },
) {
  const outside = (
    r: AnchorRect,
    box: { left: number; top: number; right: number; bottom: number },
  ) => r.bottom < box.top || r.top > box.bottom || r.right < box.left || r.left > box.right;
  if (outside(rect, { left: 0, top: 0, right: viewport.width, bottom: viewport.height }))
    return false;
  for (let p = anchor.parentElement; p && p !== document.body; p = p.parentElement) {
    const style = getComputedStyle(p);
    if (style.overflowX === 'visible' && style.overflowY === 'visible') continue;
    if (outside(rect, p.getBoundingClientRect())) return false;
  }
  return true;
}

// Один кадр на пересчёт; без requestAnimationFrame (старый движок, тест без pretendToBeVisual) — таймер.
const requestFrame = (cb: () => void): number =>
  typeof window.requestAnimationFrame === 'function'
    ? window.requestAnimationFrame(cb)
    : window.setTimeout(cb, 16);
const cancelFrame = (frame: number) =>
  typeof window.cancelAnimationFrame === 'function'
    ? window.cancelAnimationFrame(frame)
    : window.clearTimeout(frame);
