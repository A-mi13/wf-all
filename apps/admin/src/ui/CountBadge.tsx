import styles from './CountBadge.module.css';
import { cx } from './cx';

// Счётчик у пункта меню (макет 07: «Подтверждения 3»). Ноль не показывается; больше 99 — «99+».
// active — пункт выбран (фон chip-accent, цифра accent). Смысл числа передаёт подпись пункта,
// для читалки экрана — label (например «3 ожидают решения»).
export function CountBadge({
  count,
  active,
  label,
}: {
  count: number;
  active?: boolean;
  label?: string;
}) {
  if (!Number.isFinite(count) || count <= 0) return null;
  return (
    <span className={cx(styles.badge, active && styles.active)} aria-label={label}>
      {count > 99 ? '99+' : String(Math.floor(count))}
    </span>
  );
}
