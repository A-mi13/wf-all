import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import styles from './CopyButton.module.css';
import { cx } from './cx';
import { Icon } from './icon';
import { useToast } from './toast';

// Сколько держится «галочка» после копирования.
export const COPIED_MS = 2000;

// Копирование в один клик (спека UI-фундамента §4.8): id в журнале и подтверждениях, почта сотрудника,
// адрес города, geoname_id, job_id импорта. Успех — значок check на 2 с и «Скопировано» в вежливой
// живой области (без тоста — не шумит); отказ (нет разрешения, не HTTPS, нет Clipboard API) — значение
// выделено в поле только для чтения рядом (Ctrl+C руками) и тост «Скопируйте вручную». display 'icon' —
// кнопка-значок с именем label; 'text' — значок и видимая подпись. Повторный отказ нового тоста не
// добавляет (ошибку закрывают только руками — копилась бы стопка): поле уже на экране, значение
// выделяется снова. preview — только для витрины: состояние без нажатия («галочка» без таймера, поле отказа).
export function CopyButton({
  value,
  label,
  display = 'icon',
  preview,
}: {
  value: string;
  label: string;
  display?: 'icon' | 'text';
  preview?: 'copied' | 'manual';
}) {
  const t = useTranslations('ui.copy');
  const toast = useToast();
  const [copied, setCopied] = useState(preview === 'copied');
  const [manual, setManual] = useState(preview === 'manual');
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const field = useRef<HTMLInputElement>(null);
  useEffect(() => () => clearTimeout(timer.current), []);
  // отказ: поле появилось — выделить значение и поставить фокус
  useEffect(() => {
    if (!manual) return;
    field.current?.focus();
    field.current?.select();
  }, [manual]);

  const copy = async () => {
    clearTimeout(timer.current);
    try {
      if (!navigator.clipboard) throw new Error('Clipboard API недоступен');
      await navigator.clipboard.writeText(value);
    } catch {
      setCopied(false);
      if (manual) {
        field.current?.focus();
        field.current?.select();
        return;
      }
      setManual(true);
      toast.error(t('manual'));
      return;
    }
    setManual(false);
    setCopied(true);
    timer.current = setTimeout(() => setCopied(false), COPIED_MS);
  };

  return (
    <span className={styles.root}>
      <button
        type="button"
        className={cx(styles.button, display === 'text' && styles.withText)}
        aria-label={display === 'icon' ? label : undefined}
        onClick={() => void copy()}
      >
        <span className={styles.icon} data-copy-icon={copied ? 'check' : 'copy'}>
          <Icon name={copied ? 'check' : 'copy'} size={16} />
        </span>
        {display === 'text' && <span>{label}</span>}
      </button>
      {manual && (
        <input
          ref={field}
          className={styles.field}
          readOnly
          value={value}
          aria-label={label}
          onFocus={(e) => e.currentTarget.select()}
        />
      )}
      <span aria-live="polite" className={styles.srOnly} data-copy-live="">
        {copied ? t('done') : ''}
      </span>
    </span>
  );
}
