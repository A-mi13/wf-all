import styles from './Avatar.module.css';
import { cx } from './cx';

// Инициалы сотрудника (макет 07, «Кто имеет доступ»: «МК», «ИЛ»). Имя всегда стоит рядом
// текстом, поэтому аватар декоративный и для читалки экрана скрыт.
function initials(name: string): string {
  const words = name.trim().split(/\s+/).filter(Boolean);
  const letters = words
    .slice(0, 2)
    .map((w) => Array.from(w)[0] ?? '')
    .join('');
  return letters ? letters.toLocaleUpperCase() : '?';
}

export function Avatar({ name, size = 'sm' }: { name: string; size?: 'sm' | 'md' }) {
  return (
    <span className={cx(styles.avatar, size === 'md' && styles.md)} aria-hidden="true">
      {initials(name)}
    </span>
  );
}
