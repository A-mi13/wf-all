import styles from './CountBadge.module.css';
import { cx } from './cx';

// Счётчик у пункта меню (макет 07: «Подтверждения 3»). Ноль не показывается; больше 99 — «99+».
// active — пункт выбран (фон chip-accent, цифра accent). Смысл числа передаёт подпись пункта;
// label («3 ожидают решения») — текст для читалки экрана: визуально скрыт, а цифра скрыта от
// читалки (aria-label на span без роли читалки не объявляют).
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
  const text = count > 99 ? '99+' : String(Math.floor(count));
  return (
    <span className={cx(styles.badge, active && styles.active)}>
      {label ? (
        <>
          <span aria-hidden="true">{text}</span>
          <span className={styles.srOnly}>{label}</span>
        </>
      ) : (
        text
      )}
    </span>
  );
}
