import type { IconName } from '@wf/icons';
import { IconGraphic } from './Icon';
import styles from './Icon.module.css';

// Иллюстрация — иконка набора крупно (пустые состояния, спека §4.4, §4.9): всегда декоративная, предмет —
// text-muted, акцент — как у Icon. На 48 и 64 px толщина — мастер-ширина слоя (OPTICAL[48] = 1).
export function Illustration({ name, size }: { name: IconName; size: 48 | 64 }) {
  return <IconGraphic name={name} size={size} scale={1} objectClass={styles.illustrationObject} />;
}
