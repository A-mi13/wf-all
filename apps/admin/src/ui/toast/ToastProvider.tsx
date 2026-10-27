import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslations } from 'use-intl';
import { cx } from '../cx';
import { TOASTS_ATTR, topOverlayPanel } from '../overlay/stack';
import {
  TOAST_HIDE_MS,
  type ToastAction,
  type ToastApi,
  ToastContext,
  type ToastOptions,
  type ToastTone,
} from './context';
import styles from './Toast.module.css';

type Item = { id: number; tone: ToastTone; text: string; action?: ToastAction };

const TONE: Record<ToastTone, string | undefined> = {
  success: styles.success,
  error: styles.error,
  info: styles.info,
};

// Тосты админки (макеты 07 и 08): область — порталом в body, поверх оверлеев; внизу по центру, на
// телефоне — во всю ширину. Живые области постоянные (читалка экрана объявляет добавленное в уже
// существующую область): успех и справка — aria-live="polite", ошибка — aria-live="assertive" и
// без таймера. Роли status/alert не ставим: постоянный role="alert" путал бы getByRole('alert')
// экранов (FormError, «нет связи»). При открытой модалке тосты входят в цикл Tab (overlay/stack.ts).
//
// Порядок: успехи и справки — в своей области, ошибки — в своей, под ними; внутри области — в
// порядке появления. Общий порядок появления между областями не держим: узел живёт в одной
// области, а переставлять тосты стилем (order) без inline-стиля нельзя (CSP), display: contents
// у живых областей ломает их объявление в части читалок. Ошибки внизу стабильны: исчезающие
// успехи над ними их не двигают.
//
// Фокус: закрыли тост, на котором он стоял (кнопкой, действием или программно dismiss(id)), —
// фокус возвращается на элемент, с которого пришли в область тостов; его нет (удалён, выключен,
// вне верхнего диалога) — на панель верхнего диалога по стеку оверлеев; диалога нет — остаётся
// браузеру.
export function ToastProvider({ children }: { children: ReactNode }) {
  const t = useTranslations('ui.toast');
  const [items, setItems] = useState<Item[]>([]);
  const next = useRef(0);
  const nodes = useRef(new Map<number, HTMLElement>());
  const returnTo = useRef<HTMLElement | null>(null);
  const register = useCallback((id: number, el: HTMLElement) => {
    nodes.current.set(id, el);
    return () => {
      nodes.current.delete(id);
    };
  }, []);

  const api = useMemo<ToastApi>(() => {
    const restoreFocus = () => {
      const top = topOverlayPanel();
      const back = returnTo.current;
      returnTo.current = null;
      if (back?.isConnected && (!top || top.contains(back))) back.focus();
      if (back && document.activeElement === back) return;
      top?.focus();
    };
    const dismiss = (id: number) => {
      if (nodes.current.get(id)?.contains(document.activeElement)) restoreFocus();
      setItems((list) => list.filter((i) => i.id !== id));
    };
    const push = (tone: ToastTone) => (text: string, opts?: ToastOptions) => {
      const id = ++next.current;
      setItems((list) => [...list, { id, tone, text, action: opts?.action }]);
      return id;
    };
    return { success: push('success'), error: push('error'), info: push('info'), dismiss };
  }, []);

  return (
    <ToastContext value={api}>
      {children}
      {createPortal(
        <section
          className={styles.region}
          aria-label={t('region')}
          {...{ [TOASTS_ATTR]: '' }}
          onFocus={(e) => {
            // вход в область снаружи — запомнить, откуда (мышью без фокуса до того — некуда)
            const from = e.relatedTarget;
            if (from instanceof HTMLElement && e.currentTarget.contains(from)) return;
            returnTo.current = from instanceof HTMLElement ? from : null;
          }}
        >
          <div aria-live="polite" className={styles.list}>
            {items
              .filter((i) => i.tone !== 'error')
              .map((item) => (
                <Toast
                  key={item.id}
                  item={item}
                  closeLabel={t('close')}
                  dismiss={api.dismiss}
                  register={register}
                />
              ))}
          </div>
          <div aria-live="assertive" className={styles.list}>
            {items
              .filter((i) => i.tone === 'error')
              .map((item) => (
                <Toast
                  key={item.id}
                  item={item}
                  closeLabel={t('close')}
                  dismiss={api.dismiss}
                  register={register}
                />
              ))}
          </div>
        </section>,
        document.body,
      )}
    </ToastContext>
  );
}

function Toast({
  item,
  closeLabel,
  dismiss,
  register,
}: {
  item: Item;
  closeLabel: string;
  dismiss: (id: number) => void; // стабильная: новый тост не перезапускает таймеры старых
  register: (id: number, el: HTMLElement) => () => void; // узел тоста — dismiss видит его фокус
}) {
  const { id } = item;
  // фокус после закрытия возвращает dismiss провайдера
  const onDismiss = () => dismiss(id);
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const autoHide = item.tone !== 'error';
  const paused = hovered || focused;

  // Таймер скрытия: с начала после каждой паузы (успел дочитать — успеет и нажать).
  useEffect(() => {
    if (!autoHide || paused) return;
    const timer = setTimeout(() => dismiss(id), TOAST_HIDE_MS);
    return () => clearTimeout(timer);
  }, [autoHide, paused, dismiss, id]);

  return (
    <div
      ref={(el) => (el ? register(id, el) : undefined)}
      className={cx(styles.toast, TONE[item.tone])}
      onMouseEnter={() => setHovered(true)}
      onMouseLeave={() => setHovered(false)}
      onFocus={() => setFocused(true)}
      onBlur={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget)) setFocused(false);
      }}
    >
      <svg viewBox="0 0 24 24" width="17" height="17" aria-hidden="true" className={styles.icon}>
        <path
          d={item.tone === 'success' ? 'M5 12.4 L9.6 17 L19 7.4' : 'M12 7 V13 M12 16.6 V16.8'}
        />
      </svg>
      <span className={styles.text}>{item.text}</span>
      {item.action && (
        <button
          type="button"
          className={styles.action}
          onClick={() => {
            item.action?.onClick();
            onDismiss();
          }}
        >
          {item.action.label}
        </button>
      )}
      <button type="button" className={styles.close} aria-label={closeLabel} onClick={onDismiss}>
        <svg viewBox="0 0 24 24" width="12" height="12" aria-hidden="true" className={styles.icon}>
          <path d="M6 6 L18 18 M18 6 L6 18" />
        </svg>
      </button>
    </div>
  );
}
