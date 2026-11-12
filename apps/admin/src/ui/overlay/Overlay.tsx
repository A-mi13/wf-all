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
import { Button } from '../Button';
import { cx } from '../cx';
import { Icon } from '../icon';
import { useIsMobile } from '../media';
import styles from './Overlay.module.css';
import {
  focusables,
  isTopModal,
  isTopOverlay,
  pushOverlay,
  setOverlayFooter,
  trapTab,
} from './stack';

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
  // Введённое не сохранено (спека UI-фундамента §4.11): Esc, фон и «Закрыть» сперва спрашивают «Уйти без
  // сохранения?» (LeaveDialog поверх); «Уйти» — onClose. Панель в адресе (UF2b.8) закрывается в onClose
  // навигацией с ignoreBlocker: true — блокировщик формы (useLeaveGuard) второй раз не спросит.
  dirty?: boolean;
};

type Placement = 'center' | 'right' | 'bottom';

const PLACEMENT: Record<Placement, string | undefined> = {
  center: styles.center,
  right: styles.right,
  bottom: styles.bottom,
};

// Общая основа Modal, Drawer и Sheet: портал в body, role="dialog" + aria-modal, фокус внутрь при
// открытии и обратно на открыватель при закрытии, фокус-ловушка, Esc, блок прокрутки фона. Запись
// стека — модальная (stack.ts): её открытие закрывает все открытые Popover.
// Закрытый оверлей не рендерится: состояние содержимого при новом открытии чистое.
// fill — шторка во всю высоту своего предела (палитра на телефоне), а не по содержимому.
export function Overlay(
  props: OverlayProps & { placement: Placement; tone?: 'default' | 'danger'; fill?: boolean },
) {
  if (!props.open) return null;
  return createPortal(<Panel {...props} />, document.body);
}

function Panel({
  placement,
  tone = 'default',
  fill = false,
  onClose,
  title,
  description,
  children,
  footer,
  initialFocus,
  dismissible = true,
  dirty = false,
}: OverlayProps & { placement: Placement; tone?: 'default' | 'danger'; fill?: boolean }) {
  const t = useTranslations('actions');
  const [asking, setAsking] = useState(false);
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
    if (!dismissible) return;
    if (dirty) setAsking(true);
    else onClose();
  };
  // Esc — только если оверлей верхний из всех записей стека (открытый поверх список закрывается
  // первым); Tab — если он верхний модальный (немодальная запись сверху ловушку не снимает).
  const onKeyDown = useEffectEvent((e: KeyboardEvent, id: symbol) => {
    if (e.defaultPrevented) return;
    if (e.key === 'Escape' && isTopOverlay(id)) {
      e.preventDefault();
      dismiss();
    } else if (e.key === 'Tab' && isTopModal(id)) {
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

  const stackId = useRef<symbol | null>(null);
  useEffect(() => {
    const id = Symbol('overlay');
    stackId.current = id;
    // модальная запись: открытые Popover закрываются (onModalOpen)
    const release = pushOverlay(id, panel.current, { placement });
    const listener = (e: KeyboardEvent) => onKeyDown(e, id);
    document.addEventListener('keydown', listener);
    focusInitial();
    return () => {
      document.removeEventListener('keydown', listener);
      release();
      // фокус — туда, откуда открыли (кнопка в списке или в панели под подтверждением)
      if (opener?.isConnected) opener.focus();
    };
    // placement у Panel постоянен (Modal/Drawer/Sheet — свой; ConfirmDialog меняет оболочку целиком)
  }, [opener, placement]);

  // Подвал — признак записи стека (тосты над подвалом): отдельный эффект, смена подвала не переоткрывает панель.
  const footerPlacement = footer ? placement : null;
  useEffect(() => {
    if (stackId.current) setOverlayFooter(stackId.current, footerPlacement);
  }, [footerPlacement]);

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
        className={cx(
          styles.panel,
          PLACEMENT[placement],
          tone === 'danger' && styles.danger,
          fill && styles.fill,
        )}
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
            onClick={dismiss}
            disabled={!dismissible}
          >
            <Icon name="close" size={16} />
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
      <LeaveDialog
        open={asking}
        onStay={() => setAsking(false)}
        onLeave={() => {
          setAsking(false);
          onClose();
        }}
      />
    </div>
  );
}

export type LeaveDialogProps = {
  open: boolean;
  onStay: () => void; // «Остаться», Esc, фон, «Закрыть»
  onLeave: () => void;
  title?: ReactNode; // по умолчанию «Уйти без сохранения?»
  text?: ReactNode; // по умолчанию «Введённое пропадёт»
};

// Вопрос при уходе с несохранённого (§4.11): закрытие панели с dirty и переход при useLeaveGuard. Фокус по
// умолчанию — «Остаться» (случайный Enter ничего не теряет); на узком экране — шторка, как ConfirmDialog.
export function LeaveDialog({ open, onStay, onLeave, title, text }: LeaveDialogProps) {
  const t = useTranslations('ui.leave');
  const mobile = useIsMobile();
  const stay = useRef<HTMLButtonElement>(null);
  return (
    <Overlay
      open={open}
      placement={mobile ? 'bottom' : 'center'}
      onClose={onStay}
      title={title ?? t('title')}
      description={text ?? t('text')}
      initialFocus={stay}
    >
      <div className={styles.actions}>
        <Button ref={stay} variant="secondary" onClick={onStay}>
          {t('stay')}
        </Button>
        <Button variant="danger" onClick={onLeave}>
          {t('leave')}
        </Button>
      </div>
    </Overlay>
  );
}
