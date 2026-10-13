import type { ReactNode } from 'react';
import { cx } from '../cx';
import styles from './States.module.css';

export type StateTone = 'neutral' | 'accent' | 'danger' | 'warning' | 'muted';

const TONE: Record<StateTone, string | undefined> = {
  neutral: undefined,
  accent: styles.toneAccent,
  danger: styles.toneDanger,
  warning: styles.toneWarning,
  muted: styles.toneMuted,
};

// Общий блок состояния (макет 07, «Состояния»): заголовок цветом тона, пояснение, действие.
// page — состояние вместо страницы целиком: заголовок h1 (у страницы он один), крупнее.
export function StateBlock({
  tone = 'neutral',
  title,
  text,
  extra,
  action,
  page,
  dashed,
  role,
  titleAs,
}: {
  tone?: StateTone;
  title: ReactNode;
  text?: ReactNode;
  extra?: ReactNode; // строка под текстом (код ошибки)
  action?: ReactNode;
  page?: boolean;
  dashed?: boolean;
  role?: 'alert' | 'status';
  titleAs?: 'h1' | 'h2' | 'p'; // по умолчанию: страница — h1, блок в списке — абзац
}) {
  const Title = titleAs ?? (page ? 'h1' : 'p');
  return (
    <div
      role={role}
      className={cx(styles.block, page && styles.page, dashed && styles.empty, TONE[tone])}
    >
      <Title className={styles.title}>{title}</Title>
      {text && <p className={styles.text}>{text}</p>}
      {extra}
      {action && <div className={styles.action}>{action}</div>}
    </div>
  );
}
