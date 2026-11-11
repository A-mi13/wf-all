import {
  Children,
  cloneElement,
  type PointerEvent,
  type ReactElement,
  type ReactNode,
  useEffect,
  useId,
  useRef,
  useState,
} from 'react';
import { Popover, type PopoverCloseReason } from '../popover/Popover';
import type { Placement } from '../popover/position';
import styles from './Hint.module.css';

// Уход курсора с якоря на подсказку не должен её закрыть (WCAG 1.4.13 «наведение держит»): закрытие —
// с паузой, наведение на пузырь её отменяет.
export const TOOLTIP_HIDE_MS = 150;

// Подсказка (спека UI-фундамента §4.8): Popover kind 'hint' у единственного ребёнка — фокусируемого
// элемента (кнопка, ссылка, поле). Открывается наведением и фокусом, Esc скрывает (фокус остаётся),
// наведение на саму подсказку её держит; связь — aria-describedby ребёнка на подсказку (описание
// слышно и когда она скрыта). title не используется (недоступен с клавиатуры). Якорь положения и
// событий — обёртка-строка вокруг ребёнка (фокус и наведение всплывают к ней; ref ребёнка не трогаем).
// open — управляемый режим (истории, визуальный набор «две подсказки разом»). describe={false} — без
// aria-describedby, когда подсказка повторяет имя элемента (значок полосы с aria-label): иначе читалка
// слышит имя дважды. Наведение — только мышь (pointerType 'mouse'): касание шлёт совместимый mouseenter
// без mouseleave, и подсказка висела бы после перехода (ревью UF2a/I, m2, m3).
export function Tooltip({
  content,
  children,
  placement = 'bottom-start',
  open: controlled,
  describe = true,
  onEscape,
}: {
  content: ReactNode;
  children: ReactElement<{ 'aria-describedby'?: string }>;
  placement?: Placement;
  open?: boolean;
  describe?: boolean;
  onEscape?: () => void; // Esc скрыл подсказку — управляемый режим (open) снимает её сам
}) {
  const id = useId();
  const anchor = useRef<HTMLSpanElement>(null);
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState(false); // Esc — до следующего наведения или фокуса
  const hide = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(hide.current), []);

  const enter = (e: PointerEvent) => {
    if (e.pointerType !== 'mouse') return;
    clearTimeout(hide.current);
    setHovered(true);
    setDismissed(false);
  };
  const leave = (e: PointerEvent) => {
    if (e.pointerType !== 'mouse') return;
    clearTimeout(hide.current);
    hide.current = setTimeout(() => setHovered(false), TOOLTIP_HIDE_MS);
  };
  const onClose = (reason: PopoverCloseReason) => {
    if (reason === 'escape') {
      onEscape?.();
      return setDismissed(true);
    }
    clearTimeout(hide.current);
    setHovered(false);
    setFocused(false);
  };

  const child = Children.only(children);
  const described =
    [child.props['aria-describedby'], describe ? id : undefined].filter(Boolean).join(' ') ||
    undefined;
  const open = controlled ?? ((hovered || focused) && !dismissed);
  return (
    <>
      <span
        ref={anchor}
        className={styles.anchor}
        onPointerEnter={enter}
        onPointerLeave={leave}
        onFocus={() => {
          setFocused(true);
          setDismissed(false);
        }}
        onBlur={() => setFocused(false)}
      >
        {cloneElement(child, { 'aria-describedby': described })}
      </span>
      <Popover
        anchor={anchor}
        open={open}
        kind="hint"
        id={id}
        placement={placement}
        className={styles.bubble}
        onClose={onClose}
      >
        <div className={styles.tip} onPointerEnter={enter} onPointerLeave={leave}>
          {content}
        </div>
      </Popover>
    </>
  );
}
