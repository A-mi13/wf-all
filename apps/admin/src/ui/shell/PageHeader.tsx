import type { ReactNode } from 'react';
import styles from './PageHeader.module.css';

// Шапка страницы раздела (макет 07): заголовок — единственный h1 страницы, подзаголовок, справа —
// действия и чипы состояния («2ФА включена»).
export function PageHeader({
  title,
  subtitle,
  actions,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header className={styles.header}>
      <div className={styles.text}>
        <h1 className={styles.title}>{title}</h1>
        {subtitle && <p className={styles.subtitle}>{subtitle}</p>}
      </div>
      {actions && <div className={styles.actions}>{actions}</div>}
    </header>
  );
}
