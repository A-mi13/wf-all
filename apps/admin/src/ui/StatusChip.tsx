import type { ReactNode } from 'react';
import { cx } from './cx';
import styles from './StatusChip.module.css';

// Тона по макету 07 (чипы заголовка и ролей): цвет — не единственный носитель смысла, текст
// внутри обязателен.
// - success — включено, живой («2ФА включена», «владелец»);
// - accent — выделенное («админ», «пилот»);
// - neutral — справочное («модератор», «ожидание»);
// - warning — требует внимания («приглашение не принято», «в очереди»);
// - danger — отказ, блокировка, просрочено.
export type ChipTone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger';

const TONE: Record<ChipTone, string | undefined> = {
  neutral: styles.neutral,
  accent: styles.accent,
  success: styles.success,
  warning: styles.warning,
  danger: styles.danger,
};

export function StatusChip({
  tone = 'neutral',
  children,
  className,
}: {
  tone?: ChipTone;
  children: ReactNode;
  className?: string;
}) {
  return <span className={cx(styles.chip, TONE[tone], className)}>{children}</span>;
}
