import {
  type ReactNode,
  type RefObject,
  useEffect,
  useEffectEvent,
  useId,
  useRef,
  useState,
} from 'react';
import { createPortal } from 'react-dom';
import { useTranslations } from 'use-intl';
import { cx } from '../cx';
import styles from './Overlay.module.css';
import { focusables, isTopOverlay, pushOverlay, trapTab } from './stack';

export type OverlayProps = {
  open: boolean;
  onClose: () => void; // Esc, «Закрыть», клик по фону
  title: ReactNode;
  description?: ReactNode; // текст под заголовком, aria-describedby
  children?: ReactNode;
  footer?: ReactNode; // кнопки действий
  // Куда поставить фокус при открытии; по умолчанию — первый интерактивный элемент содержимого,
  // нет такого — сам диалог.
  initialFocus?: RefObject<HTMLElement | null>;
  dismissible?: boolean; // false — Esc и клик по фону не закрывают (идёт запрос)
};

type Placement = 'center' | 'right' | 'bottom';

const PLACEMENT: Record<Placement, string | undefined> = {
  center: styles.center,
  right: styles.right,
  bottom: styles.bottom,
};

// Общая основа Modal, Drawer и Sheet: портал в body, role="dialog" + aria-modal, фокус внутрь при
// открытии и обратно на открыватель при закрытии, фокус-ловушка, Esc, блок прокрутки фона.
// Закрытый оверлей не рендерится: состояние содержимого при новом открытии чистое.
export function Overlay(
  props: OverlayProps & { placement: Placement; tone?: 'default' | 'danger' },
) {
  if (!props.open) return null;
  return createPortal(<Panel {...props} />, document.body);
}

function Panel({
  placement,
  tone = 'default',
  onClose,
  title,
  description,
  children,
  footer,
  initialFocus,
  dismissible = true,
}: OverlayProps & { placement: Placement; tone?: 'default' | 'danger' }) {
  const t = useTranslations('ui');
  const titleId = useId();
  const descriptionId = useId();
  const panel = useRef<HTMLDivElement>(null);
  const body = useRef<HTMLDivElement>(null);
  // Открыватель — во время первого рендера, до монтирования содержимого: autoFocus поля внутри
  // срабатывает раньше эффектов, и в эффекте activeElement был бы уже полем диалога.
  const [opener] = useState(() =>
    document.activeElement instanceof HTMLElement ? document.activeElement : null,
  );

  const dismiss = () => {
    if (dismissible) onClose();
  };
  const onKeyDown = useEffectEvent((e: KeyboardEvent, id: symbol) => {
    if (e.defaultPrevented || !isTopOverlay(id)) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      dismiss();
    } else if (e.key === 'Tab') {
      trapTab(e, panel.current);
    }
  });
  // Начальный фокус ставится один раз. В StrictMode (dev, main.tsx) эффект открытия повторяется
  // после уборки, а уборка вернула фокус открывателю — повтор ставит его на тот же элемент, что и
  // первый раз (поле с autoFocus), а не на первый элемент содержимого.
  const initial = useRef<HTMLElement | null>(null);
  const focusInitial = useEffectEvent(() => {
    const root = panel.current;
    const inside = (el: Element | null): el is HTMLElement =>
      el instanceof HTMLElement && Boolean(root?.contains(el));
    const placed = initial.current;
    if (placed?.isConnected && inside(placed)) {
      if (!inside(document.activeElement)) placed.focus();
      return;
    }
    // фокус уже внутри (autoFocus содержимого) — не перебиваем
    const target = inside(document.activeElement)
      ? document.activeElement
      : (initialFocus?.current ?? focusables(body.current)[0] ?? root);
    if (target !== document.activeElement) target?.focus();
    initial.current = target ?? null;
  });

  useEffect(() => {
    const id = Symbol('overlay');
    const release = pushOverlay(id, panel.current);
    const listener = (e: KeyboardEvent) => onKeyDown(e, id);
    document.addEventListener('keydown', listener);
    focusInitial();
    return () => {
      document.removeEventListener('keydown', listener);
      release();
      // фокус — туда, откуда открыли (кнопка в списке или в панели под подтверждением)
      if (opener?.isConnected) opener.focus();
    };
  }, [opener]);

  return (
    <div className={styles.root}>
      <div className={styles.backdrop} aria-hidden="true" onClick={dismiss} />
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={description ? descriptionId : undefined}
        tabIndex={-1}
        className={cx(styles.panel, PLACEMENT[placement], tone === 'danger' && styles.danger)}
      >
        {placement === 'bottom' && <div className={styles.handle} aria-hidden="true" />}
        <div className={styles.header}>
          <h2 id={titleId} className={styles.title}>
            {title}
          </h2>
          <button
            type="button"
            className={styles.close}
            aria-label={t('close')}
            onClick={onClose}
            disabled={!dismissible}
          >
            <svg
              viewBox="0 0 24 24"
              width="16"
              height="16"
              aria-hidden="true"
              className={styles.icon}
            >
              <path d="M6 6 L18 18 M18 6 L6 18" />
            </svg>
          </button>
        </div>
        <div ref={body} className={styles.body}>
          {description && (
            <p id={descriptionId} className={styles.description}>
              {description}
            </p>
          )}
          {children}
        </div>
        {footer && <div className={styles.footer}>{footer}</div>}
      </div>
    </div>
  );
}
