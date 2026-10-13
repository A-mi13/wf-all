import { type ReactNode, useEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useTranslations } from 'use-intl';
import { cx } from '../cx';
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

// Тосты админки (макеты 07 и 08): область — порталом в body, поверх оверлеев; внизу по центру, на телефоне — во всю ширину. Успех и
// справка — role="status" (вежливо), ошибка — role="alert" и без таймера.
export function ToastProvider({ children }: { children: ReactNode }) {
  const t = useTranslations('ui.toast');
  const [items, setItems] = useState<Item[]>([]);
  const next = useRef(0);

  const api = useMemo<ToastApi>(() => {
    const dismiss = (id: number) => setItems((list) => list.filter((i) => i.id !== id));
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
        <section className={styles.region} aria-label={t('region')}>
          {items.map((item) => (
            <Toast key={item.id} item={item} closeLabel={t('close')} dismiss={api.dismiss} />
          ))}
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
}: {
  item: Item;
  closeLabel: string;
  dismiss: (id: number) => void; // стабильная: новый тост не перезапускает таймеры старых
}) {
  const { id } = item;
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
      role={item.tone === 'error' ? 'alert' : 'status'}
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
